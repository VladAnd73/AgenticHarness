package watch

import "testing"

func TestLatestReleaseParses(t *testing.T) {
	fakeGH(t, `echo '{"tagName":"v2.5.0","url":"https://github.com/o/r/releases/tag/v2.5.0","publishedAt":"2026-07-21T10:00:00Z"}'`)
	rel, found, err := LatestRelease(t.TempDir(), "o/r")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("want found")
	}
	if rel.TagName != "v2.5.0" || rel.URL != "https://github.com/o/r/releases/tag/v2.5.0" {
		t.Fatalf("bad parse: %+v", rel)
	}
}

func TestLatestReleaseNoReleasesIsBenign(t *testing.T) {
	// gh release view exits non-zero with "release not found" on stderr when a
	// repo has zero releases. That is benign nothing-to-report, not an error.
	fakeGH(t, `echo "release not found" >&2; exit 1`)
	_, found, err := LatestRelease(t.TempDir(), "o/empty")
	if err != nil {
		t.Fatalf("no-releases repo must not error, got %v", err)
	}
	if found {
		t.Fatal("no-releases repo must report found=false")
	}
}

func TestLatestReleaseRealErrorPropagates(t *testing.T) {
	fakeGH(t, `echo "gh: authentication failed" >&2; exit 4`)
	if _, _, err := LatestRelease(t.TempDir(), "o/r"); err == nil {
		t.Fatal("a non-benign gh error must propagate")
	}
}
