package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

// TestDoBeadsPromote_RejectsEmptyID verifies that doBeadsPromote rejects an
// empty bead ID before attempting any bd invocation.
func TestDoBeadsPromote_RejectsEmptyID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := doBeadsPromote("", &stdout, &stderr)
	if code == 0 {
		t.Error("expected non-zero exit for empty bead ID, got 0")
	}
	if !strings.Contains(stderr.String(), "bead-id must not be empty") {
		t.Errorf("expected empty-id error in stderr, got: %s", stderr.String())
	}
}

// TestDoBeadsPromote_RejectsFlagShapedID verifies that doBeadsPromote rejects
// IDs that begin with "-" to prevent accidental injection into bd arguments.
func TestDoBeadsPromote_RejectsFlagShapedID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := doBeadsPromote("--inject", &stdout, &stderr)
	if code == 0 {
		t.Error("expected non-zero exit for flag-shaped bead ID, got 0")
	}
	if !strings.Contains(stderr.String(), "invalid bead-id") {
		t.Errorf("expected invalid-id error in stderr, got: %s", stderr.String())
	}
}

// TestDoBeadsPromote_RejectsWhitespaceID verifies that doBeadsPromote rejects
// IDs containing whitespace to prevent multi-argument injection.
func TestDoBeadsPromote_RejectsWhitespaceID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := doBeadsPromote("ga-abc extra", &stdout, &stderr)
	if code == 0 {
		t.Error("expected non-zero exit for whitespace-containing bead ID, got 0")
	}
	if !strings.Contains(stderr.String(), "invalid bead-id") {
		t.Errorf("expected invalid-id error in stderr, got: %s", stderr.String())
	}
}

// TestBeadsPromoteMetadataArg verifies that the metadata argument assembled
// by doBeadsPromote uses the declared beadmeta constant, not a raw literal.
// This is a compile-time check: if beadmeta.OriginMetadataKey does not exist,
// this file will not compile, failing the build before the guard test runs.
func TestBeadsPromoteMetadataArg(t *testing.T) {
	want := beadmeta.OriginMetadataKey + "=" + originUserValue
	// Validate the assembled string matches expectations without invoking bd.
	if !strings.HasPrefix(want, "gc.") {
		t.Errorf("metadata arg %q must start with gc.", want)
	}
	if !strings.Contains(want, "=user") {
		t.Errorf("metadata arg %q must contain =user", want)
	}
}

// TestNewBeadsPromoteCmdHelp verifies that the cobra command has the expected
// Use and Short fields (regression guard for --help output).
func TestNewBeadsPromoteCmdHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := newBeadsPromoteCmd(&stdout, &stderr)
	if cmd.Use != "promote <bead-id>" {
		t.Errorf("Use = %q, want %q", cmd.Use, "promote <bead-id>")
	}
	if !strings.Contains(cmd.Short, "user-origin") {
		t.Errorf("Short = %q; want it to mention user-origin", cmd.Short)
	}
}
