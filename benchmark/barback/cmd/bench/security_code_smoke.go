// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

//go:build !benchmark_legacy

package main

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	whatsmeow "github.com/polymorfa/hypermeow"
	"github.com/polymorfa/hypermeow/proto/waFingerprint"
	"github.com/polymorfa/hypermeow/types"
)

func validateIdentityVerificationCodes(ctx context.Context, client *whatsmeow.Client, userID types.JID) error {
	if userID.Server != types.HiddenUserServer {
		return whatsmeow.ErrIdentityVerificationRequiresLID
	}
	codes, err := client.GetIdentityVerificationCodes(ctx, userID)
	if err != nil {
		return fmt.Errorf("generate identity verification codes: %w", err)
	}
	if codes.UserID != userID || len(codes.NumericCode) != 60 {
		return errors.New("identity verification result has an invalid LID or numeric code")
	}
	for _, digit := range codes.NumericCode {
		if digit < '0' || digit > '9' {
			return errors.New("identity verification numeric code contains non-digits")
		}
	}
	var display, verification waFingerprint.CombinedFingerprint
	if err = proto.Unmarshal(codes.DisplayQRCode, &display); err != nil {
		return fmt.Errorf("decode display QR: %w", err)
	}
	if err = proto.Unmarshal(codes.VerificationQRCode, &verification); err != nil {
		return fmt.Errorf("decode verification QR: %w", err)
	}
	if len(display.GetLocalFingerprint().GetPublicKey()) != 0 || len(display.GetRemoteFingerprint().GetPublicKey()) != 0 {
		return errors.New("display QR exposed unhashed identity keys")
	}
	if len(verification.GetLocalFingerprint().GetPublicKey()) == 0 || len(verification.GetRemoteFingerprint().GetPublicKey()) == 0 {
		return errors.New("verification QR omitted identity keys")
	}
	return nil
}
