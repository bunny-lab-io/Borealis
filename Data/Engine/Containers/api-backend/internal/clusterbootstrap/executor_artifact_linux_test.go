package clusterbootstrap

import (
	"strings"
	"testing"
)

func TestArtifactExecutorRetainsContainment(t *testing.T) {
	args, err := ArtifactPreparationExecutorArguments("/var/tmp/borealis-bootstrap-root.ABC1234567/manager", sessionFixture().Nonce)
	if err != nil {
		t.Fatal(err)
	}
	command := strings.Join(args, "\n")
	for _, required := range []string{"--pipe", "--wait", "--collect", "--service-type=notify", "--property=KillMode=control-group", "--property=WatchdogSec=2s", "--property=WatchdogSignal=SIGKILL", "--property=TimeoutStopSec=2s", "--property=RuntimeMaxSec=31min", "--property=LimitCORE=0", "bootstrap-artifact-session-contained"} {
		if !strings.Contains(command, required) {
			t.Fatal("missing containment", required)
		}
	}
	for _, changed := range append([]string{"valid", "old runtime"}, executorProperties...) {
		t.Run(changed, func(t *testing.T) {
			values := executorFixtureValues()
			values["RuntimeMaxUSec"] = "31min"
			if changed == "old runtime" {
				values["RuntimeMaxUSec"] = "6min"
			} else if changed != "valid" {
				values[changed] = "unsafe"
			}
			group := "0::/system.slice/" + executorFixtureValues()["Id"] + "\n"
			proof, err := verifyExecutorIdentityFor(sessionFixture().Nonce, strings.Repeat("a", 32), "11111111-1111-4111-8111-111111111111", group, 123, values, true)
			if (err == nil) != (changed == "valid") {
				t.Fatal("incorrect artifact supervision", proof, err)
			}
			if changed == "valid" {
				if _, err := verifyExecutorIdentity(sessionFixture().Nonce, strings.Repeat("a", 32), "11111111-1111-4111-8111-111111111111", group, 123, values); err == nil {
					t.Fatal("ordinary guard accepted artifact runtime ceiling")
				}
			}
		})
	}
}
