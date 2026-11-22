package version

import "testing"

func TestOnlyATagTty1CanPlaceIsRead(t *testing.T) {
	for _, tag := range []string{"v0.1.0", "v1.2.3", "v10.20.30", "v0.1.0-rc.1", "v1.0.0-alpha", "v1.0.0-alpha.1", "v1.0.0-0.3.7", "v1.0.0-x-y-z.1", "v999999999.0.0"} {
		v, err := Parse(tag)
		if err != nil {
			t.Errorf("%s: wanted it read, got %v", tag, err)
		} else if v.String() != tag {
			t.Errorf("%s: wanted it written back the same, got %s", tag, v)
		}
	}
	for _, tag := range []string{
		"", "v", "1.2.3", "V1.2.3", "v1", "v1.2", "v1.2.3.4", "v1.2.x", "v-1.2.3", "v1.2.-3", "v1.2.3 ", " v1.2.3",
		"v01.2.3", "v1.02.3", "v1.2.03", "v1.2.3-", "v1.2.3-rc..1", "v1.2.3-rc.01", "v1.2.3-rc_1", "v1.2.3+build", "v1.2.3-rc.1+build",
		"v1.2.3\n", "v1.2.3-é", "v1000000000.0.0", "v99999999999999999999.0.0", "latest", "main", "../v1.2.3",
	} {
		if v, err := Parse(tag); err == nil {
			t.Errorf("%q: wanted it refused, got %s", tag, v)
		}
	}
}

// Older to newer. Every one is older than every one after it, and the same as itself.
func TestReleasesAreOrderedAsSemanticVersioningOrdersThem(t *testing.T) {
	order := []string{
		"v0.0.1", "v0.1.0-rc.1", "v0.1.0-rc.2", "v0.1.0-rc.10", "v0.1.0", "v0.1.1", "v0.2.0", "v0.10.0",
		"v1.0.0-0", "v1.0.0-1", "v1.0.0-1.1", "v1.0.0-alpha", "v1.0.0-alpha.1", "v1.0.0-alpha.beta", "v1.0.0-beta", "v1.0.0-beta.2",
		"v1.0.0-beta.11", "v1.0.0-rc.1", "v1.0.0", "v1.0.1", "v2.0.0-rc.1", "v2.0.0", "v10.0.0",
	}
	for i, a := range order {
		for j, b := range order {
			va, err := Parse(a)
			if err != nil {
				t.Fatal(err)
			}
			vb, err := Parse(b)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := va.Compare(vb), sign(i-j); got != want {
				t.Errorf("%s against %s: wanted %d, got %d", a, b, want, got)
			}
		}
	}
}
