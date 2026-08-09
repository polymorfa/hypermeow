package whatsmeow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/socket"
	"go.mau.fi/whatsmeow/types"
)

const maxBusinessCoverPhotoBytes = 5 * 1024 * 1024

type businessCoverUploadResponse struct {
	MetaHMAC  string `json:"meta_hmac"`
	FBID      string `json:"fbid"`
	Timestamp string `json:"ts"`
}

var businessProfileDays = map[string]struct{}{
	"sun": {}, "mon": {}, "tue": {}, "wed": {}, "thu": {}, "fri": {}, "sat": {},
}

var businessProfileHourModes = map[string]struct{}{
	"specific_hours": {}, "open_24h": {}, "appointment_only": {},
}

func buildBusinessProfileDelta(update types.BusinessProfileUpdate) (waBinary.Node, error) {
	if update.Address == nil && update.Email == nil && update.Description == nil && update.Websites == nil && update.Hours == nil {
		return waBinary.Node{}, fmt.Errorf("business profile update is empty")
	}
	if update.Address != nil && len(*update.Address) > 512 {
		return waBinary.Node{}, fmt.Errorf("business address exceeds 512 bytes")
	}
	if update.Description != nil && len(*update.Description) > 1024 {
		return waBinary.Node{}, fmt.Errorf("business description exceeds 1024 bytes")
	}
	if update.Email != nil {
		if len(*update.Email) > 320 {
			return waBinary.Node{}, fmt.Errorf("business email exceeds 320 bytes")
		}
		if *update.Email != "" {
			parsed, err := mail.ParseAddress(*update.Email)
			if err != nil || parsed.Address != *update.Email {
				return waBinary.Node{}, fmt.Errorf("business email is invalid")
			}
		}
	}

	children := make([]waBinary.Node, 0, 7)
	if update.Address != nil {
		children = append(children, waBinary.Node{Tag: "address", Content: []byte(*update.Address)})
	}
	if update.Email != nil {
		children = append(children, waBinary.Node{Tag: "email", Content: []byte(*update.Email)})
	}
	if update.Description != nil {
		children = append(children, waBinary.Node{Tag: "description", Content: []byte(*update.Description)})
	}
	if update.Websites != nil {
		if len(*update.Websites) < 1 || len(*update.Websites) > 2 {
			return waBinary.Node{}, fmt.Errorf("business profile must contain between 1 and 2 websites")
		}
		for _, website := range *update.Websites {
			if len(website) > 2048 {
				return waBinary.Node{}, fmt.Errorf("business website exceeds 2048 bytes")
			}
			parsed, err := url.ParseRequestURI(website)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return waBinary.Node{}, fmt.Errorf("business website %q is not an absolute HTTP URL", website)
			}
			children = append(children, waBinary.Node{Tag: "website", Content: []byte(website)})
		}
	}
	if update.Hours != nil {
		hours, err := buildBusinessHoursNode(*update.Hours)
		if err != nil {
			return waBinary.Node{}, err
		}
		children = append(children, hours)
	}

	return waBinary.Node{
		Tag: "business_profile",
		Attrs: waBinary.Attrs{
			"v":             "3",
			"mutation_type": "delta",
		},
		Content: children,
	}, nil
}

func buildBusinessHoursNode(update types.BusinessHoursUpdate) (waBinary.Node, error) {
	if update.TimeZone == "" || len(update.TimeZone) > 128 {
		return waBinary.Node{}, fmt.Errorf("business hours timezone is invalid")
	}
	if _, err := time.LoadLocation(update.TimeZone); err != nil {
		return waBinary.Node{}, fmt.Errorf("business hours timezone is invalid: %w", err)
	}
	if len(update.Days) < 1 || len(update.Days) > 7 {
		return waBinary.Node{}, fmt.Errorf("business hours must contain between 1 and 7 days")
	}

	seen := make(map[string]struct{}, len(update.Days))
	configs := make([]waBinary.Node, 0, len(update.Days))
	for _, day := range update.Days {
		if _, ok := businessProfileDays[day.DayOfWeek]; !ok {
			return waBinary.Node{}, fmt.Errorf("invalid business hours day %q", day.DayOfWeek)
		}
		if _, ok := seen[day.DayOfWeek]; ok {
			return waBinary.Node{}, fmt.Errorf("duplicate business hours day %q", day.DayOfWeek)
		}
		seen[day.DayOfWeek] = struct{}{}
		if _, ok := businessProfileHourModes[day.Mode]; !ok {
			return waBinary.Node{}, fmt.Errorf("invalid business hours mode %q", day.Mode)
		}

		attrs := waBinary.Attrs{"day_of_week": day.DayOfWeek, "mode": day.Mode}
		if day.Mode == "specific_hours" {
			if day.OpenTime < 0 || day.OpenTime > 1439 || day.CloseTime < 0 || day.CloseTime > 1439 || day.OpenTime == day.CloseTime {
				return waBinary.Node{}, fmt.Errorf("invalid specific hours for %s", day.DayOfWeek)
			}
			attrs["open_time"] = strconv.Itoa(day.OpenTime)
			attrs["close_time"] = strconv.Itoa(day.CloseTime)
		} else if day.OpenTime != 0 || day.CloseTime != 0 {
			return waBinary.Node{}, fmt.Errorf("%s mode does not accept open or close times", day.Mode)
		}
		configs = append(configs, waBinary.Node{Tag: "business_hours_config", Attrs: attrs})
	}

	return waBinary.Node{
		Tag:     "business_hours",
		Attrs:   waBinary.Attrs{"timezone": strings.TrimSpace(update.TimeZone)},
		Content: configs,
	}, nil
}

func (cli *Client) UpdateBusinessProfile(ctx context.Context, update types.BusinessProfileUpdate) error {
	node, err := buildBusinessProfileDelta(update)
	if err != nil {
		return err
	}
	_, err = cli.sendIQ(ctx, infoQuery{
		Namespace: "w:biz",
		Type:      iqSet,
		To:        types.ServerJID,
		Content:   []waBinary.Node{node},
	})
	if err != nil {
		return fmt.Errorf("failed to update business profile: %w", err)
	}
	return nil
}

func validateBusinessCoverPhoto(image []byte) ([]byte, error) {
	if len(image) == 0 {
		return nil, fmt.Errorf("business cover photo is empty")
	}
	if len(image) > maxBusinessCoverPhotoBytes {
		return nil, fmt.Errorf("business cover photo exceeds %d bytes", maxBusinessCoverPhotoBytes)
	}
	mimeType := http.DetectContentType(image)
	if mimeType != "image/jpeg" && mimeType != "image/png" {
		return nil, fmt.Errorf("business cover photo must be JPEG or PNG")
	}
	hash := sha256.Sum256(image)
	return hash[:], nil
}

func (cli *Client) uploadBusinessCoverPhoto(ctx context.Context, image []byte) (businessCoverUploadResponse, error) {
	var response businessCoverUploadResponse
	hash, err := validateBusinessCoverPhoto(image)
	if err != nil {
		return response, err
	}
	mediaConn, err := cli.refreshMediaConn(ctx, false)
	if err != nil {
		return response, fmt.Errorf("failed to refresh media connections: %w", err)
	}
	if len(mediaConn.Hosts) == 0 {
		return response, fmt.Errorf("media connection response contained no upload hosts")
	}

	token := base64.URLEncoding.EncodeToString(hash)
	query := url.Values{"auth": {mediaConn.Auth}, "token": {token}}
	uploadURL := url.URL{
		Scheme:   "https",
		Host:     mediaConn.Hosts[0].Hostname,
		Path:     "/pps/biz-cover-photo/" + token,
		RawQuery: query.Encode(),
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL.String(), bytes.NewReader(image))
	if err != nil {
		return response, fmt.Errorf("failed to prepare business cover photo upload: %w", err)
	}
	request.ContentLength = int64(len(image))
	request.Header.Set("Content-Type", http.DetectContentType(image))
	request.Header.Set("Origin", socket.Origin)
	request.Header.Set("Referer", socket.Origin+"/")

	httpResponse, err := cli.mediaHTTP.Do(request)
	if err != nil {
		return response, fmt.Errorf("failed to upload business cover photo: %w", err)
	}
	defer drainAndClose(httpResponse.Body)
	if httpResponse.StatusCode != http.StatusOK {
		return response, fmt.Errorf("business cover photo upload failed with status code %d", httpResponse.StatusCode)
	}
	if err = json.NewDecoder(httpResponse.Body).Decode(&response); err != nil {
		return response, fmt.Errorf("failed to parse business cover photo upload response: %w", err)
	}
	if _, err = buildBusinessCoverPhotoUpdateNode(response); err != nil {
		return response, err
	}
	return response, nil
}

func buildBusinessCoverPhotoUpdateNode(response businessCoverUploadResponse) (waBinary.Node, error) {
	if response.MetaHMAC == "" || response.FBID == "" || response.Timestamp == "" {
		return waBinary.Node{}, fmt.Errorf("business cover photo upload response is incomplete")
	}
	return waBinary.Node{
		Tag: "cover_photo",
		Attrs: waBinary.Attrs{
			"id":    response.FBID,
			"op":    "update",
			"token": response.MetaHMAC,
			"ts":    response.Timestamp,
		},
	}, nil
}

func buildBusinessCoverPhotoDeleteNode(coverID string) (waBinary.Node, error) {
	if strings.TrimSpace(coverID) == "" {
		return waBinary.Node{}, fmt.Errorf("business cover photo ID is empty")
	}
	if len(coverID) > 256 {
		return waBinary.Node{}, fmt.Errorf("business cover photo ID exceeds 256 bytes")
	}
	return waBinary.Node{
		Tag:   "cover_photo",
		Attrs: waBinary.Attrs{"id": coverID, "op": "delete"},
	}, nil
}

func (cli *Client) SetBusinessCoverPhoto(ctx context.Context, image []byte) (string, error) {
	response, err := cli.uploadBusinessCoverPhoto(ctx, image)
	if err != nil {
		return "", err
	}
	node, err := buildBusinessCoverPhotoUpdateNode(response)
	if err != nil {
		return "", err
	}
	_, err = cli.sendIQ(ctx, infoQuery{
		Namespace: "w:biz",
		Type:      iqSet,
		To:        types.ServerJID,
		Content:   []waBinary.Node{node},
	})
	if err != nil {
		return "", fmt.Errorf("failed to set business cover photo: %w", err)
	}
	return response.FBID, nil
}

func (cli *Client) DeleteBusinessCoverPhoto(ctx context.Context, coverID string) error {
	node, err := buildBusinessCoverPhotoDeleteNode(coverID)
	if err != nil {
		return err
	}
	_, err = cli.sendIQ(ctx, infoQuery{
		Namespace: "w:biz",
		Type:      iqSet,
		To:        types.ServerJID,
		Content:   []waBinary.Node{node},
	})
	if err != nil {
		return fmt.Errorf("failed to delete business cover photo: %w", err)
	}
	return nil
}
