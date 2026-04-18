/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

// Package policy translates a Task's declared permissions into the
// concrete Kubernetes resources that enforce them: a Role + RoleBinding
// for RBAC, a NetworkPolicy for egress, and volume specs for Secret
// projection. Everything here is pure (no cluster I/O besides DNS
// resolution for egress hostnames) so it can be table-tested.
package policy
