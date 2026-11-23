package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// synthFixturePath is the JSON subscription the synthetic client's transforms are tested on
// (internal/synth). It is generated here, from the real generator, so a change of the
// subscription's shape (tags, balancer, the Bamboo outbound) fails this test and the fixture
// is refreshed together with the transforms: go test ./cmd/api -run TestSynthFixture -update-golden
var synthFixturePath = filepath.Join("..", "..", "internal", "synth", "testdata", "happ.json")

// synthFixtureBody: «⚡ Авто» and «🎋 Бамбук 1/2» through the Hub and two entries, by name with
// the DNS section — what the test user gets today.
func synthFixtureBody() []byte {
	d := directRouting{
		byName: true, dnsTunnel: true,
		sites:    []string{"domain:bank.example"},
		ips:      []string{"geoip:private", "geoip:ru", "198.51.100.7/32"},
		fronting: []string{"domain:front.example"},
	}
	return jsonSubscription(autoInputs(), d)
}

func TestSynthFixtureMatchesGenerator(t *testing.T) {
	want := synthFixtureBody()
	if len(want) == 0 {
		t.Fatal("the generator returned no JSON subscription")
	}
	if *updateGolden {
		if err := os.WriteFile(synthFixturePath, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(synthFixturePath)
	if err != nil {
		t.Fatalf("read the fixture: %v (run with -update-golden)", err)
	}
	if !bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(want)) {
		t.Fatalf("internal/synth/testdata/happ.json is not what the generator serves any more: refresh it with -update-golden and check internal/synth against the new shape")
	}
}
