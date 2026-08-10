package barback_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestComparisonSecurityCodeSmoke(t *testing.T) {
	tests := []struct {
		name      string
		variant   string
		buildTags string
		requested string
		want      string
	}{
		{name: "candidate", variant: "hypermeow", requested: "true", want: "true"},
		{name: "legacy candidate", variant: "hypermeow", buildTags: "benchmark_legacy", requested: "true", want: "false"},
		{name: "legacy candidate among tags", variant: "hypermeow", buildTags: "integration,benchmark_legacy", requested: "true", want: "false"},
		{name: "upstream baseline", variant: "whatsmeow", buildTags: "benchmark_legacy", requested: "true", want: "false"},
		{name: "disabled", variant: "hypermeow", requested: "false", want: "false"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := `. ./comparison-matrix-env.sh; comparison_security_code_smoke "$1" "$2" "$3"`
			output, err := exec.Command("bash", "-c", command, "bash", test.variant, test.buildTags, test.requested).CombinedOutput()
			if err != nil {
				t.Fatalf("evaluate security-code smoke: %v: %s", err, output)
			}
			if got := strings.TrimSpace(string(output)); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}
