/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package policy

import "errors"

// PermanentError marks a spec problem that retrying cannot fix: a
// malformed CIDR, a hostname that authoritatively does not exist
// (NXDOMAIN), or config that can never be projected. Anything else —
// resolver timeouts, SERVFAIL, network blips — is left unwrapped so
// callers retry instead of failing the Task.
type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// IsPermanent reports whether err (or anything it wraps) is a
// PermanentError, i.e. the Task should be failed instead of requeued.
func IsPermanent(err error) bool {
	var perm *PermanentError
	return errors.As(err, &perm)
}
