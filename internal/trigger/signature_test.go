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
	"testing"
)

func hmacHex(secret, body []byte) string {
	h := hmac.New(sha256.New, secret)
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func TestVerifyHMAC(t *testing.T) {
	secret := []byte("s3cret")
	body := []byte(`{"action":"opened"}`)
	valid := hmacHex(secret, body)

	cases := []struct {
		name    string
		algo    string
		prefix  string
		header  string
		body    []byte
		secret  []byte
		wantErr error // nil = success; otherwise the expected sentinel
	}{
		{name: "bare hex", header: valid, body: body, secret: secret},
		{name: "github-style prefix", prefix: "sha256=", header: "sha256=" + valid, body: body, secret: secret},
		{name: "explicit algo", algo: "sha256", header: valid, body: body, secret: secret},
		{name: "missing header", header: "", body: body, secret: secret, wantErr: ErrMissingSignature},
		{name: "empty secret", header: valid, body: body, secret: nil, wantErr: ErrEmptySecret},
		{name: "unsupported algo", algo: "sha1", header: valid, body: body, secret: secret, wantErr: ErrUnsupportedAlgorithm},
		{name: "tampered body", header: valid, body: []byte(`{"action":"closed"}`), secret: secret, wantErr: ErrSignatureMismatch},
		{name: "wrong secret", header: valid, body: body, secret: []byte("wrong"), wantErr: ErrSignatureMismatch},
		{name: "prefix needed but not configured", prefix: "", header: "sha256=" + valid, body: body, secret: secret, wantErr: ErrSignatureMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyHMAC(tc.algo, tc.prefix, tc.header, tc.secret, tc.body)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("VerifyHMAC(%q): unexpected error %v", tc.name, err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("VerifyHMAC(%q): err=%v, want %v", tc.name, err, tc.wantErr)
			}
		})
	}
}
