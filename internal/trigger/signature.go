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

// These are the sentinel verification errors. The first two are caller
// failures, and the handler responds 401. The last two are operator
// configuration problems that the caller cannot fix, and the handler
// responds 500.
var (
	ErrMissingSignature     = errors.New("missing signature header")
	ErrSignatureMismatch    = errors.New("signature mismatch")
	ErrEmptySecret          = errors.New("empty signing secret")
	ErrUnsupportedAlgorithm = errors.New("unsupported signature algorithm")
)

// VerifyHMAC compares a provided header signature against HMAC(secret, body).
//
// `algorithm` accepts "sha256". The function treats a blank value as
// sha256, because the CRD defaults it. The function strips `prefix` from
// the header value before the comparison, which matches the GitHub
// "sha256=<hex>" convention. The comparison is constant-time to prevent
// timing leaks.
//
// The function is minimal by design. The threat model of the adapter is
// "public HTTPS endpoint + shared secret". Users who need OAuth or mTLS
// terminate that at the ingress.
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
