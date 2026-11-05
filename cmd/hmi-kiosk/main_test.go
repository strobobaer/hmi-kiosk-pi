//go:build linux

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSelftestWithoutServiceExitsWithTwo(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runSelftest(nil, &out, &errb); code != 2 {
		t.Fatalf("Exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "nicht erreichbar") || out.Len() != 0 {
		t.Fatalf("stdout %q, stderr %q", out.String(), errb.String())
	}
}

func TestSoakRejectsBadArguments(t *testing.T) {
	for _, args := range [][]string{{"-duration", "0s"}, {"-duration", "-1h"}, {"-nope"}, {"-duration", "abc"}} {
		var out, errb bytes.Buffer
		if code := runSoak(args, &out, &errb); code != 2 {
			t.Errorf("%v: Exit %d", args, code)
		}
	}
}
