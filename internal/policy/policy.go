// Package policy turns OCI annotations into a validated, typed Policy value.
//
// Every annotation value is UNTRUSTED input (see docs/security.md): it arrives
// from OCI annotations the container author controls. Parse therefore validates
// each field and rejects anything malformed or unsafe — a mount path that could
// escape the container rootfs, a bucket name with illegal characters, or an
// endpoint with an unexpected scheme. An opted-in container whose policy fails
// validation must be failed closed by the caller, never started with a partial
// or attacker-influenced configuration.
package policy

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/randomizedcoder/fuse-filesystem-inject/internal/contract"
)

// Policy is a validated GeeSFS injection request.
type Policy struct {
	Bucket   string // S3 bucket to mount
	Mount    string // absolute, cleaned mount path inside the container
	Endpoint string // S3 endpoint URL (http/https)
}

// bucketRE matches DNS-style S3 bucket names: 3–63 chars, lowercase
// alphanumerics plus '-' and '.', starting and ending with an alphanumeric.
// It deliberately excludes anything shell- or path-dangerous (whitespace,
// quotes, '/', '$', …).
var bucketRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

// allowedSchemes is the endpoint scheme allowlist.
var allowedSchemes = map[string]bool{"http": true, "https": true}

// Parse reads the GeeSFS policy from OCI annotations. Its three outcomes map to
// the three things the caller can do:
//
//	matched=false, err=nil  → the container did not opt in; pass through.
//	matched=true,  err!=nil → the container opted in but is misconfigured;
//	                          the caller MUST fail closed.
//	matched=true,  err=nil  → a validated Policy to inject.
func Parse(annotations map[string]string) (p Policy, matched bool, err error) {
	if annotations[contract.AnnEnabled] != "true" {
		return Policy{}, false, nil
	}

	p = Policy{
		Bucket:   annotations[contract.AnnBucket],
		Mount:    annotations[contract.AnnMount],
		Endpoint: annotations[contract.AnnEndpoint],
	}

	if err := Validate(p); err != nil {
		return Policy{}, true, err
	}

	return p, true, nil
}

// Validate checks the fields of an already-assembled Policy. It is the single
// definition of what a safe policy is, shared by Parse (the geesefs-runc entry
// point) and geesefsd, which re-validates every mount request as defense in
// depth before it does anything privileged (setns + mount).
func Validate(p Policy) error {
	if err := validateBucket(p.Bucket); err != nil {
		return err
	}
	if err := validateMount(p.Mount); err != nil {
		return err
	}
	return validateEndpoint(p.Endpoint)
}

func validateBucket(bucket string) error {
	switch {
	case bucket == "":
		return fmt.Errorf("%s is required when %s=true", contract.AnnBucket, contract.AnnEnabled)
	case !bucketRE.MatchString(bucket):
		return fmt.Errorf("%s %q is not a valid S3 bucket name", contract.AnnBucket, bucket)
	case strings.Contains(bucket, ".."):
		return fmt.Errorf("%s %q must not contain consecutive dots", contract.AnnBucket, bucket)
	}
	return nil
}

// validateMount enforces that the mount target is an absolute, already-clean
// path that cannot escape the container rootfs. Requiring path.Clean(m) == m
// rejects "..", ".", trailing slashes, and doubled separators in one check.
func validateMount(mount string) error {
	switch {
	case mount == "":
		return fmt.Errorf("%s is required when %s=true", contract.AnnMount, contract.AnnEnabled)
	case !path.IsAbs(mount):
		return fmt.Errorf("%s %q must be an absolute path", contract.AnnMount, mount)
	case path.Clean(mount) != mount:
		return fmt.Errorf("%s %q is not a clean path (no '..', '.', or redundant separators)", contract.AnnMount, mount)
	case mount == "/":
		return fmt.Errorf("%s must not be the container root %q", contract.AnnMount, "/")
	}
	return nil
}

func validateEndpoint(endpoint string) error {
	if endpoint == "" {
		return fmt.Errorf("%s is required when %s=true", contract.AnnEndpoint, contract.AnnEnabled)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("%s %q is not a valid URL: %w", contract.AnnEndpoint, endpoint, err)
	}
	switch {
	case !allowedSchemes[u.Scheme]:
		return fmt.Errorf("%s %q must use scheme http or https", contract.AnnEndpoint, endpoint)
	case u.Host == "":
		return fmt.Errorf("%s %q is missing a host", contract.AnnEndpoint, endpoint)
	case u.User != nil:
		return fmt.Errorf("%s must not embed credentials", contract.AnnEndpoint)
	case u.Path != "" && u.Path != "/":
		return fmt.Errorf("%s %q must not include a path", contract.AnnEndpoint, endpoint)
	case u.RawQuery != "" || u.Fragment != "":
		return fmt.Errorf("%s %q must not include a query or fragment", contract.AnnEndpoint, endpoint)
	}
	return nil
}
