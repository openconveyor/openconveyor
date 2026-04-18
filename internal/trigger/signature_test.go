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
		wantErr bool
	}{
		{name: "bare hex", header: valid, body: body, secret: secret},
		{name: "github-style prefix", prefix: "sha256=", header: "sha256=" + valid, body: body, secret: secret},
		{name: "explicit algo", algo: "sha256", header: valid, body: body, secret: secret},
		{name: "missing header", header: "", body: body, secret: secret, wantErr: true},
		{name: "empty secret", header: valid, body: body, secret: nil, wantErr: true},
		{name: "unsupported algo", algo: "sha1", header: valid, body: body, secret: secret, wantErr: true},
		{name: "tampered body", header: valid, body: []byte(`{"action":"closed"}`), secret: secret, wantErr: true},
		{name: "wrong secret", header: valid, body: body, secret: []byte("wrong"), wantErr: true},
		{name: "prefix needed but not configured", prefix: "", header: "sha256=" + valid, body: body, secret: secret, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyHMAC(tc.algo, tc.prefix, tc.header, tc.secret, tc.body)
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Fatalf("VerifyHMAC(%q): err=%v wantErr=%v", tc.name, err, tc.wantErr)
			}
		})
	}
}
