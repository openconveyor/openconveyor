/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

// Package policy translates the declared permissions of a Task into the
// Kubernetes resources that enforce them. The resources are a Role and a
// RoleBinding for RBAC, a NetworkPolicy for egress, and volume specs for
// Secret projection. The package is pure: it does no cluster I/O except
// DNS resolution for egress hostnames, so tests can table-test it.
package policy
