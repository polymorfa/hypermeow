package whatsmeow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/socket"
	"go.mau.fi/whatsmeow/types"
)

const (
	businessGraphQLEndpoint         = "https://graph.facebook.com/graphql"
	businessAddProductDocumentID    = "24249359867999500"
	businessEditProductDocumentID   = "9889773371084956"
	businessDeleteProductDocumentID = "9376108569185474"
	businessTokenRequestTimeout     = 30 * time.Second
	maxBusinessGraphQLResponseBytes = 4 * 1024 * 1024
	maxBusinessProductImageBytes    = 16 * 1024 * 1024
)

var (
	ErrBusinessTokenRecoveryRequired = errors.New("business access token recovery is required on the primary device")
	ErrBusinessTokenTooManyAttempts  = errors.New("business access token request was rate limited")
	errBusinessIncorrectNonce        = errors.New("business access token nonce was rejected")
)

const businessNonceDeliveredAttr = "__whatsmeow_business_nonce_delivered"

type businessAccessToken struct {
	accessToken string
	actorID     string
}

type businessNonceWaiter struct {
	ch chan string
}

type businessCatalogAuthState struct {
	tokenLock   chan struct{}
	token       businessAccessToken
	nonceWaiter atomic.Pointer[businessNonceWaiter]
}

type businessGraphQLErrorItem struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

type businessGraphQLError struct {
	StatusCode int
	Errors     []businessGraphQLErrorItem
}

func (err *businessGraphQLError) Error() string {
	if len(err.Errors) == 0 {
		return fmt.Sprintf("business GraphQL request failed with status %d", err.StatusCode)
	}
	codes := make([]string, 0, len(err.Errors))
	for _, item := range err.Errors {
		codes = append(codes, strconv.Itoa(item.Code))
	}
	return "business GraphQL request failed with error code(s) " + strings.Join(codes, ",")
}

func isBusinessGraphQLAuthError(err error) bool {
	var graphErr *businessGraphQLError
	if !errors.As(err, &graphErr) {
		return false
	}
	for _, item := range graphErr.Errors {
		if item.Code == 190 || item.Code == 400 {
			return true
		}
	}
	return false
}

func validateBusinessProductInput(input types.BusinessProductInput) error {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return fmt.Errorf("business product name is empty")
	}
	if len(input.Name) > 256 {
		return fmt.Errorf("business product name exceeds 256 bytes")
	}
	if len(input.Description) > 4096 {
		return fmt.Errorf("business product description exceeds 4096 bytes")
	}
	if len(input.RetailerID) > 256 {
		return fmt.Errorf("business product retailer ID exceeds 256 bytes")
	}
	if len(input.ImageURLs) < 1 || len(input.ImageURLs) > 10 {
		return fmt.Errorf("business product must contain between 1 and 10 images")
	}
	for _, rawURL := range input.ImageURLs {
		if err := validateBusinessMediaURL(rawURL); err != nil {
			return fmt.Errorf("invalid business product image URL: %w", err)
		}
	}
	if len(input.VideoURLs) > 10 {
		return fmt.Errorf("business product cannot contain more than 10 videos")
	}
	for _, rawURL := range input.VideoURLs {
		if err := validateBusinessMediaURL(rawURL); err != nil {
			return fmt.Errorf("invalid business product video URL: %w", err)
		}
	}
	if input.URL != "" {
		parsed, err := url.ParseRequestURI(input.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || len(input.URL) > 2048 {
			return fmt.Errorf("business product URL must be an absolute HTTPS URL of at most 2048 bytes")
		}
	}
	if input.Price == "" {
		if input.Currency != "" || input.SalePrice != "" {
			return fmt.Errorf("business product currency and sale price require a price")
		}
	} else {
		if !isUppercaseCurrency(input.Currency) {
			return fmt.Errorf("business product currency must be a three-letter uppercase code")
		}
		if !isUnsignedDecimal(input.Price) {
			return fmt.Errorf("business product price must be an integer amount in thousandths")
		}
		if input.SalePrice != "" && !isUnsignedDecimal(input.SalePrice) {
			return fmt.Errorf("business product sale price must be an integer amount in thousandths")
		}
	}
	if input.ComplianceCategory != "" && len(input.ComplianceCategory) > 128 {
		return fmt.Errorf("business product compliance category exceeds 128 bytes")
	}
	if input.Compliance != nil {
		if len(input.Compliance.CountryCodeOrigin) > 3 || len(input.Compliance.ImporterName) > 256 {
			return fmt.Errorf("business product compliance information is invalid")
		}
		if address := input.Compliance.ImporterAddress; address != nil {
			if len(address.Street1) > 512 || len(address.Street2) > 512 || len(address.City) > 256 || len(address.Region) > 256 || len(address.PostalCode) > 64 || len(address.CountryCode) > 3 {
				return fmt.Errorf("business product importer address is invalid")
			}
		}
	}
	return nil
}

func isUnsignedDecimal(value string) bool {
	if value == "" || len(value) > 18 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func isUppercaseCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, char := range value {
		if char < 'A' || char > 'Z' {
			return false
		}
	}
	return true
}

func validateBusinessMediaURL(rawURL string) error {
	if len(rawURL) > 4096 {
		return fmt.Errorf("URL exceeds 4096 bytes")
	}
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return fmt.Errorf("URL must be absolute HTTPS")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "whatsapp.net" && !strings.HasSuffix(host, ".whatsapp.net") && host != "fbcdn.net" && !strings.HasSuffix(host, ".fbcdn.net") && host != "facebook.com" && !strings.HasSuffix(host, ".facebook.com") {
		return fmt.Errorf("URL must use a WhatsApp or Meta media host")
	}
	return nil
}

func buildBusinessProductInfo(input types.BusinessProductInput) map[string]any {
	images := make([]map[string]any, len(input.ImageURLs))
	for index, imageURL := range input.ImageURLs {
		images[index] = map[string]any{"url": imageURL}
	}
	media := map[string]any{"image": images}
	if len(input.VideoURLs) > 0 {
		videos := make([]map[string]any, len(input.VideoURLs))
		for index, videoURL := range input.VideoURLs {
			videos[index] = map[string]any{"url": videoURL}
		}
		media["video"] = videos
	}
	info := map[string]any{
		"name":      strings.TrimSpace(input.Name),
		"media":     media,
		"is_hidden": input.Hidden,
	}
	if input.Description != "" {
		info["description"] = input.Description
	}
	if input.URL != "" {
		info["url"] = input.URL
	}
	if input.RetailerID != "" {
		info["retailer_id"] = input.RetailerID
	}
	if input.Price != "" {
		info["currency"] = input.Currency
		info["price"] = input.Price
	}
	if input.SalePrice != "" {
		info["sale_price"] = input.SalePrice
	}
	if input.Compliance != nil {
		compliance := map[string]any{"country_code_origin": input.Compliance.CountryCodeOrigin}
		if input.Compliance.ImporterName != "" {
			compliance["importer_name"] = input.Compliance.ImporterName
		}
		if address := input.Compliance.ImporterAddress; address != nil {
			addressInput := map[string]any{
				"country_code": address.CountryCode,
				"city":         address.City,
				"street1":      address.Street1,
			}
			if address.Street2 != "" {
				addressInput["street2"] = address.Street2
			}
			if address.Region != "" {
				addressInput["region"] = address.Region
			}
			if address.PostalCode != "" {
				addressInput["postal_code"] = address.PostalCode
			}
			compliance["importer_address"] = addressInput
		}
		info["compliance_info"] = compliance
	}
	if input.ComplianceCategory != "" {
		info["compliance_category"] = input.ComplianceCategory
	}
	return info
}

func buildBusinessProductMutationVariables(jid types.JID, productID string, input types.BusinessProductInput, width, height int) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	if productID != "" {
		if err := validateBusinessID("product", productID); err != nil {
			return nil, err
		}
	}
	if err := validateBusinessProductInput(input); err != nil {
		return nil, err
	}
	width, height, err := normalizeDimensions(width, height)
	if err != nil {
		return nil, err
	}
	product := map[string]any{
		"biz_jid":      jid.ToNonAD().String(),
		"width":        width,
		"height":       height,
		"product_info": buildBusinessProductInfo(input),
	}
	if productID != "" {
		product["product_id"] = productID
	}
	return map[string]any{"input": map[string]any{"product": product}}, nil
}

func buildDeleteBusinessProductsVariables(jid types.JID, productIDs []string) (map[string]any, error) {
	if err := validateBusinessJID(jid); err != nil {
		return nil, err
	}
	if len(productIDs) < 1 || len(productIDs) > 100 {
		return nil, fmt.Errorf("business product delete must contain between 1 and 100 IDs")
	}
	seen := make(map[string]struct{}, len(productIDs))
	for _, productID := range productIDs {
		if err := validateBusinessID("product", productID); err != nil {
			return nil, err
		}
		if _, exists := seen[productID]; exists {
			return nil, fmt.Errorf("duplicate product ID %q", productID)
		}
		seen[productID] = struct{}{}
	}
	return map[string]any{"input": map[string]any{
		"biz_jid":     jid.ToNonAD().String(),
		"product_ids": productIDs,
	}}, nil
}

func decodeBusinessProductMutation(data json.RawMessage, discriminator string) (*types.BusinessProduct, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode business product mutation response: %w", err)
	}
	raw, ok := envelope[discriminator]
	if !ok {
		return nil, fmt.Errorf("business product mutation response is missing %s", discriminator)
	}
	var result struct {
		Product *types.BusinessProduct `json:"product"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", discriminator, err)
	}
	if result.Product == nil || result.Product.ID == "" {
		return nil, fmt.Errorf("%s response is missing product", discriminator)
	}
	return result.Product, nil
}

func decodeDeleteBusinessProducts(data json.RawMessage) (int, error) {
	var envelope struct {
		Result *struct {
			DeletedCount *int `json:"deleted_count"`
		} `json:"xfb_whatsapp_catalog_delete_product"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return 0, fmt.Errorf("decode business product delete response: %w", err)
	}
	if envelope.Result == nil || envelope.Result.DeletedCount == nil || *envelope.Result.DeletedCount < 0 {
		return 0, fmt.Errorf("business product delete response is missing deleted_count")
	}
	return *envelope.Result.DeletedCount, nil
}

func businessSilentNonceQuery() infoQuery {
	return infoQuery{Namespace: "fb:thrift_iq", Type: iqGet, To: types.ServerJID, SMaxID: "118", NoRetry: true, Timeout: businessTokenRequestTimeout}
}

func businessTokenExchangeQuery(nonce string) (infoQuery, error) {
	if strings.TrimSpace(nonce) == "" || len(nonce) > 8192 {
		return infoQuery{}, fmt.Errorf("business access token nonce is invalid")
	}
	return infoQuery{
		Namespace: "fb:thrift_iq",
		Type:      iqGet,
		To:        types.ServerJID,
		SMaxID:    "104",
		NoRetry:   true,
		Timeout:   businessTokenRequestTimeout,
		Content:   []waBinary.Node{{Tag: "parameters", Content: []waBinary.Node{{Tag: "code", Content: []byte(nonce)}}}},
	}, nil
}

func parseBusinessTokenResponse(node *waBinary.Node) (businessAccessToken, error) {
	if node == nil {
		return businessAccessToken{}, fmt.Errorf("business access token response is empty")
	}
	accessTokenNode, ok := node.GetOptionalChildByTag("access_token")
	if !ok {
		return businessAccessToken{}, fmt.Errorf("business access token response is missing access_token")
	}
	personNode, ok := node.GetOptionalChildByTag("business_person")
	if !ok {
		return businessAccessToken{}, fmt.Errorf("business access token response is missing business_person")
	}
	accessToken, ok := accessTokenNode.Content.([]byte)
	if !ok || len(accessToken) == 0 || len(accessToken) > 16384 {
		return businessAccessToken{}, fmt.Errorf("business access token response contains an invalid token")
	}
	actorID := personNode.AttrGetter().String("id")
	if actorID == "" || len(actorID) > 256 {
		return businessAccessToken{}, fmt.Errorf("business access token response contains an invalid business person")
	}
	return businessAccessToken{accessToken: string(accessToken), actorID: actorID}, nil
}

func (cli *Client) getBusinessCatalogAuth() *businessCatalogAuthState {
	if existing := cli.businessCatalogAuth.Load(); existing != nil {
		return existing
	}
	created := &businessCatalogAuthState{tokenLock: make(chan struct{}, 1)}
	created.tokenLock <- struct{}{}
	if cli.businessCatalogAuth.CompareAndSwap(nil, created) {
		return created
	}
	return cli.businessCatalogAuth.Load()
}

func (cli *Client) handleBusinessCatalogNotification(node *waBinary.Node) {
	state := cli.businessCatalogAuth.Load()
	if state == nil {
		return
	}
	nonceNode, ok := node.GetOptionalChildByTag("wa_ad_account_nonce")
	if !ok {
		return
	}
	nonce, ok := nonceNode.Content.([]byte)
	if !ok || len(nonce) == 0 || len(nonce) > 8192 {
		return
	}
	waiter := state.nonceWaiter.Load()
	if waiter == nil {
		return
	}
	select {
	case waiter.ch <- string(nonce):
	default:
	}
}

func (cli *Client) handleQueuedBusinessCatalogNotification(node *waBinary.Node) {
	if delivered, _ := node.Attrs[businessNonceDeliveredAttr].(bool); !delivered {
		cli.handleBusinessCatalogNotification(node)
	}
}

func parseBusinessNonceRequestResponse(node *waBinary.Node) error {
	result, ok := node.GetOptionalChildByTag("result")
	if !ok {
		return fmt.Errorf("business nonce response is missing result")
	}
	switch result.AttrGetter().String("status") {
	case "Success":
		return nil
	case "RecoveryRequired":
		return ErrBusinessTokenRecoveryRequired
	default:
		return fmt.Errorf("business nonce request returned an unknown status")
	}
}

func classifyBusinessTokenExchangeError(node *waBinary.Node, err error) error {
	if node != nil {
		if errorNode, ok := node.GetOptionalChildByTag("error"); ok {
			switch errorNode.AttrGetter().String("code") {
			case "432":
				return errBusinessIncorrectNonce
			case "431":
				return ErrBusinessTokenTooManyAttempts
			}
		}
	}
	return err
}

func (cli *Client) acquireBusinessAccessToken(ctx context.Context, state *businessCatalogAuthState) (businessAccessToken, error) {
	waitCtx, cancel := context.WithTimeout(ctx, businessTokenRequestTimeout)
	defer cancel()
	waiter := &businessNonceWaiter{ch: make(chan string, 1)}
	state.nonceWaiter.Store(waiter)
	defer state.nonceWaiter.CompareAndSwap(waiter, nil)

	response, err := cli.sendIQ(waitCtx, businessSilentNonceQuery())
	if err != nil {
		return businessAccessToken{}, fmt.Errorf("request business access token nonce: %w", err)
	}
	if err = parseBusinessNonceRequestResponse(response); err != nil {
		return businessAccessToken{}, err
	}

	var nonce string
	select {
	case nonce = <-waiter.ch:
	case <-waitCtx.Done():
		return businessAccessToken{}, fmt.Errorf("wait for business access token nonce: %w", waitCtx.Err())
	}
	exchange, err := businessTokenExchangeQuery(nonce)
	if err != nil {
		return businessAccessToken{}, err
	}
	response, err = cli.sendIQ(waitCtx, exchange)
	if err != nil {
		return businessAccessToken{}, classifyBusinessTokenExchangeError(response, err)
	}
	return parseBusinessTokenResponse(response)
}

func (cli *Client) businessAccessToken(ctx context.Context) (businessAccessToken, error) {
	state := cli.getBusinessCatalogAuth()
	select {
	case <-state.tokenLock:
		defer func() { state.tokenLock <- struct{}{} }()
	case <-ctx.Done():
		return businessAccessToken{}, ctx.Err()
	}
	if state.token.accessToken != "" {
		return state.token, nil
	}
	var token businessAccessToken
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		token, err = cli.acquireBusinessAccessToken(ctx, state)
		if !errors.Is(err, errBusinessIncorrectNonce) {
			break
		}
	}
	if err != nil {
		return businessAccessToken{}, err
	}
	state.token = token
	return token, nil
}

func (cli *Client) invalidateBusinessAccessToken(ctx context.Context, token string) error {
	state := cli.businessCatalogAuth.Load()
	if state == nil {
		return nil
	}
	select {
	case <-state.tokenLock:
	case <-ctx.Done():
		return ctx.Err()
	}
	if state.token.accessToken == token {
		state.token = businessAccessToken{}
	}
	state.tokenLock <- struct{}{}
	return nil
}

func (cli *Client) sendBusinessFacebookGraphQL(ctx context.Context, endpoint, documentID, accessToken string, variables map[string]any) (json.RawMessage, error) {
	if cli == nil {
		return nil, ErrClientIsNil
	}
	if cli.mediaHTTP == nil {
		return nil, fmt.Errorf("business GraphQL HTTP client is not configured")
	}
	body := struct {
		AccessToken string         `json:"access_token"`
		DocumentID  string         `json:"doc_id"`
		Variables   map[string]any `json:"variables"`
		Locale      string         `json:"locale"`
	}{AccessToken: accessToken, DocumentID: documentID, Variables: variables, Locale: "en_US"}
	var encoded bytes.Buffer
	if err := json.NewEncoder(&encoded).Encode(body); err != nil {
		return nil, fmt.Errorf("encode business GraphQL request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &encoded)
	if err != nil {
		return nil, fmt.Errorf("prepare business GraphQL request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", socket.Origin)
	request.Header.Set("Referer", socket.Origin+"/")
	if jid := cli.Store.GetJID(); !jid.IsEmpty() && jid.Device > 0 {
		request.Header.Set("X-WA-Device-ID", strconv.FormatUint(uint64(jid.Device), 10))
	}
	response, err := cli.mediaHTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("execute business GraphQL request: %w", err)
	}
	defer drainAndClose(response.Body)
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxBusinessGraphQLResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read business GraphQL response: %w", err)
	}
	if len(raw) > maxBusinessGraphQLResponseBytes {
		return nil, fmt.Errorf("business GraphQL response exceeds %d bytes", maxBusinessGraphQLResponseBytes)
	}
	var envelope struct {
		Data   json.RawMessage            `json:"data"`
		Errors []businessGraphQLErrorItem `json:"errors"`
		Error  *businessGraphQLErrorItem  `json:"error"`
	}
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode business GraphQL response: %w", err)
	}
	if envelope.Error != nil {
		envelope.Errors = append(envelope.Errors, *envelope.Error)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || len(envelope.Errors) > 0 {
		return nil, &businessGraphQLError{StatusCode: response.StatusCode, Errors: envelope.Errors}
	}
	if len(envelope.Data) == 0 || bytes.Equal(envelope.Data, []byte("null")) {
		return nil, fmt.Errorf("business GraphQL response is missing data")
	}
	return envelope.Data, nil
}

func businessProductMutationVariablesWithActor(variables map[string]any, actorID string) (map[string]any, error) {
	if strings.TrimSpace(actorID) == "" {
		return nil, fmt.Errorf("business product mutation actor ID is empty")
	}
	input, ok := variables["input"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("business product mutation variables are missing input")
	}
	result := make(map[string]any, len(variables))
	for key, value := range variables {
		result[key] = value
	}
	actorInput := make(map[string]any, len(input)+1)
	for key, value := range input {
		actorInput[key] = value
	}
	actorInput["actor_id"] = actorID
	result["input"] = actorInput
	return result, nil
}

func (cli *Client) executeBusinessProductMutation(ctx context.Context, documentID string, variables map[string]any) (json.RawMessage, error) {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := cli.businessAccessToken(ctx)
		if err != nil {
			return nil, err
		}
		requestVariables, err := businessProductMutationVariablesWithActor(variables, token.actorID)
		if err != nil {
			return nil, err
		}
		data, err := cli.sendBusinessFacebookGraphQL(ctx, businessGraphQLEndpoint, documentID, token.accessToken, requestVariables)
		if err == nil {
			return data, nil
		}
		if attempt == 0 && isBusinessGraphQLAuthError(err) {
			if err = cli.invalidateBusinessAccessToken(ctx, token.accessToken); err != nil {
				return nil, err
			}
			continue
		}
		return nil, err
	}
	return nil, fmt.Errorf("business product mutation failed after token refresh")
}

func (cli *Client) ownBusinessJID() (types.JID, error) {
	if cli == nil {
		return types.EmptyJID, ErrClientIsNil
	}
	jid := cli.Store.GetJID().ToNonAD()
	if err := validateBusinessJID(jid); err != nil {
		return types.EmptyJID, fmt.Errorf("business product mutation requires a paired client: %w", err)
	}
	return jid, nil
}

func (cli *Client) CreateBusinessProduct(ctx context.Context, input types.BusinessProductInput, width, height int) (*types.BusinessProduct, error) {
	jid, err := cli.ownBusinessJID()
	if err != nil {
		return nil, err
	}
	variables, err := buildBusinessProductMutationVariables(jid, "", input, width, height)
	if err != nil {
		return nil, err
	}
	data, err := cli.executeBusinessProductMutation(ctx, businessAddProductDocumentID, variables)
	if err != nil {
		return nil, fmt.Errorf("create business product: %w", err)
	}
	return decodeBusinessProductMutation(data, "xfb_whatsapp_catalog_add_product")
}

func (cli *Client) UpdateBusinessProduct(ctx context.Context, productID string, input types.BusinessProductInput, width, height int) (*types.BusinessProduct, error) {
	jid, err := cli.ownBusinessJID()
	if err != nil {
		return nil, err
	}
	variables, err := buildBusinessProductMutationVariables(jid, productID, input, width, height)
	if err != nil {
		return nil, err
	}
	data, err := cli.executeBusinessProductMutation(ctx, businessEditProductDocumentID, variables)
	if err != nil {
		return nil, fmt.Errorf("update business product: %w", err)
	}
	return decodeBusinessProductMutation(data, "xfb_whatsapp_catalog_edit_product")
}

func (cli *Client) DeleteBusinessProducts(ctx context.Context, productIDs []string) (int, error) {
	jid, err := cli.ownBusinessJID()
	if err != nil {
		return 0, err
	}
	variables, err := buildDeleteBusinessProductsVariables(jid, productIDs)
	if err != nil {
		return 0, err
	}
	data, err := cli.executeBusinessProductMutation(ctx, businessDeleteProductDocumentID, variables)
	if err != nil {
		return 0, fmt.Errorf("delete business products: %w", err)
	}
	return decodeDeleteBusinessProducts(data)
}

func validateBusinessProductImage(image []byte) ([]byte, error) {
	if len(image) == 0 {
		return nil, fmt.Errorf("business product image is empty")
	}
	if len(image) > maxBusinessProductImageBytes {
		return nil, fmt.Errorf("business product image exceeds %d bytes", maxBusinessProductImageBytes)
	}
	mimeType := http.DetectContentType(image)
	if mimeType != "image/jpeg" && mimeType != "image/png" {
		return nil, fmt.Errorf("business product image must be JPEG or PNG")
	}
	hash := sha256.Sum256(image)
	return hash[:], nil
}

func (cli *Client) UploadBusinessProductImage(ctx context.Context, image []byte) (string, error) {
	hash, err := validateBusinessProductImage(image)
	if err != nil {
		return "", err
	}
	mediaConn, err := cli.refreshMediaConn(ctx, false)
	if err != nil {
		return "", fmt.Errorf("refresh media connection for business product image: %w", err)
	}
	if len(mediaConn.Hosts) == 0 {
		return "", fmt.Errorf("media connection response contained no upload hosts")
	}
	token := base64.URLEncoding.EncodeToString(hash)
	query := url.Values{"auth": {mediaConn.Auth}, "token": {token}}
	uploadURL := url.URL{Scheme: "https", Host: mediaConn.Hosts[0].Hostname, Path: "/product/image/" + token, RawQuery: query.Encode()}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL.String(), bytes.NewReader(image))
	if err != nil {
		if urlErr, ok := err.(*url.Error); ok {
			err = urlErr.Err
		}
		return "", fmt.Errorf("prepare business product image upload: %w", err)
	}
	request.ContentLength = int64(len(image))
	request.Header.Set("Content-Type", "application/octet-stream")
	request.Header.Set("Origin", socket.Origin)
	request.Header.Set("Referer", socket.Origin+"/")
	response, err := cli.mediaHTTP.Do(request)
	if err != nil {
		if urlErr, ok := err.(*url.Error); ok {
			err = urlErr.Err
		}
		return "", fmt.Errorf("upload business product image: %w", err)
	}
	defer drainAndClose(response.Body)
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("business product image upload failed with status code %d", response.StatusCode)
	}
	var upload UploadResponse
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&upload); err != nil {
		return "", fmt.Errorf("decode business product image upload response: %w", err)
	}
	if upload.URL != "" {
		if err = validateBusinessMediaURL(upload.URL); err != nil {
			return "", fmt.Errorf("business product image upload returned an invalid URL: %w", err)
		}
		return upload.URL, nil
	}
	if !strings.HasPrefix(upload.DirectPath, "/") || len(upload.DirectPath) > 4096 {
		return "", fmt.Errorf("business product image upload response is missing a valid URL")
	}
	return "https://mmg.whatsapp.net" + upload.DirectPath, nil
}
