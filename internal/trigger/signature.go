/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package trigger

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strings"
)

// algorithmSHA256 is the only supported signature algorithm; the CRD
// defaults spec.signature.algorithm to it (clustertriggerclass_types.go).
const algorithmSHA256 = "sha256"

// Sentinel verification errors. The first two are caller failures
// (respond 401); the last two are operator configuration problems the
// caller cannot fix (respond 500).
var (
	ErrMissingSignature     = errors.New("missing signature header")
	ErrSignatureMismatch    = errors.New("signature mismatch")
	ErrEmptySecret          = errors.New("empty signing secret")
	ErrUnsupportedAlgorithm = errors.New("unsupported signature algorithm")
)

// VerifyHMAC compares a provided header signature against HMAC(secret, body).
//
// `algorithm` accepts "sha256" (blank is treated as sha256 since the CRD
// defaults it). `prefix` is stripped from the header value before
// comparison, matching GitHub's "sha256=<hex>" convention. Comparison
// is constant-time to prevent timing leaks.
//
// This is deliberately minimal — the adapter's threat model is "public
// HTTPS endpoint + shared secret"; users who need OAuth or mTLS terminate
// that at the ingress.
func VerifyHMAC(algorithm, prefix, headerValue string, secret, body []byte) error {
	if headerValue == "" {
		return ErrMissingSignature
	}
	if len(secret) == 0 {
		return ErrEmptySecret
	}

	sig := strings.TrimPrefix(strings.TrimSpace(headerValue), prefix)

	var h hash.Hash
	switch strings.ToLower(strings.TrimSpace(algorithm)) {
	case "", algorithmSHA256:
		h = hmac.New(sha256.New, secret)
	default:
		return fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, algorithm)
	}

	if _, err := h.Write(body); err != nil {
		return fmt.Errorf("hmac: %w", err)
	}
	want := hex.EncodeToString(h.Sum(nil))

	if !hmac.Equal([]byte(strings.ToLower(sig)), []byte(want)) {
		return ErrSignatureMismatch
	}
	return nil
}
