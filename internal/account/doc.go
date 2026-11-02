// Package account contains the bank account domain model and the service that
// enforces its rules (non-negative balances, atomic transfers, idempotency).
//
// The service depends on a repository interface declared here and implemented
// by package storage.
package account
