package pr

import "errors"

// Sentinel errors returned by the PR action service.
var (
	ErrInvalidPR        = errors.New("pr: invalid identity")
	ErrPRNotFound       = errors.New("pr: not found")
	ErrPRNotMergeable   = errors.New("pr: not mergeable")
	ErrPRHeadChanged    = errors.New("pr: head changed")
	ErrPRPreconditions  = errors.New("pr: merge preconditions unmet")
	ErrNothingToResolve = errors.New("pr: nothing to resolve")
	// ErrPRProviderUnavailable marks a failed provider refresh: the PR could
	// not be re-observed (network outage, rate limit, timeout), which is not
	// the same as the PR not existing and must not surface as "Unknown PR".
	ErrPRProviderUnavailable = errors.New("pr: provider unavailable")
	// ErrInvalidAction is an invalid retarget request, such as a missing or
	// self-referential child pull request number.
	ErrInvalidAction = errors.New("pr: invalid action")
	// ErrPRRejected is the provider refusing a well-formed mutation, such as a
	// base change onto a branch it will not take.
	ErrPRRejected = errors.New("pr: provider rejected mutation")
	// ErrUnsupported is a mutation the PR's SCM provider cannot express.
	ErrUnsupported = errors.New("pr: unsupported by provider")
)
