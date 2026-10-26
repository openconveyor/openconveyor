/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package policy

import "errors"

// PermanentError marks a spec problem that a retry cannot fix. Examples
// are a malformed CIDR, a hostname that authoritatively does not exist
// (NXDOMAIN), and config that the controller can never project. The
// package does not wrap other errors, such as resolver timeouts, SERVFAIL
// and transient network failures, so callers retry instead of failing
// the Task.
type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// IsPermanent reports whether err, or an error that it wraps, is a
// PermanentError. In that case the caller fails the Task instead of
// requeuing it.
func IsPermanent(err error) bool {
	var perm *PermanentError
	return errors.As(err, &perm)
}
