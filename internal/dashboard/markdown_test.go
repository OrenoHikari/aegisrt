package dashboard

import (
	"strings"
	"testing"
)

func TestRenderMarkdownSafeSupportsReportDialectAndSanitizes(t *testing.T) {
	input := []byte("# Report\n\n**Fact** and *context* with `code`.\n\n- one\n- two\n\n> verified\n\n| Paper | Metric |\n|---|---|\n| [P1] | MAE |\n\n[Safe](https://example.com) [Bad](javascript:alert(1)) <script>alert(2)</script>\n")
	rendered := RenderMarkdownSafe(input)
	for _, expected := range []string{"<h1>Report</h1>", "<strong>Fact</strong>", "<em>context</em>", "<code>code</code>", "<ul>", "<blockquote>", "<table>", `href="#paper-P1"`, `href="https://example.com"`} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("rendered Markdown missing %q: %s", expected, rendered)
		}
	}
	for _, forbidden := range []string{"<script>", "javascript:", "onerror="} {
		if strings.Contains(strings.ToLower(rendered), forbidden) {
			t.Fatalf("unsafe content %q survived: %s", forbidden, rendered)
		}
	}
	if !strings.Contains(rendered, "&lt;script&gt;") {
		t.Fatalf("raw HTML was not escaped: %s", rendered)
	}
}
