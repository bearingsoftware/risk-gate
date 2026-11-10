package tiers

import "testing"

func TestMatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pattern, path string
		want          bool
	}{
		{"**/*.md", "README.md", true},
		{"**/*.md", "docs/a/b.md", true},
		{"**/*.md", "a.mdx", false},
		{"**/*.md", "src/a.go", false},
		{"*.md", "README.md", true},
		{"*.md", "docs/guide.md", true},
		{"docs/**", "docs/a.md", true},
		{"docs/**", "docs/a/b/c.md", true},
		{"docs/**", "docs", false},
		{"docs/**", "src/docs/a.md", false},
		{"docs/**", "mydocs/a.md", false},
		{"Dockerfile*", "Dockerfile", true},
		{"Dockerfile*", "Dockerfile.dev", true},
		{"Dockerfile*", "services/api/Dockerfile", true},
		{"Dockerfile*", "services/api/Dockerfile.prod", true},
		{"Dockerfile*", "MyDockerfile", false},
		{"/Dockerfile*", "Dockerfile", true},
		{"/Dockerfile*", "services/Dockerfile", false},
		{"Jenkinsfile", "Jenkinsfile", true},
		{"Jenkinsfile", "ci/Jenkinsfile", true},
		{".github/workflows/**", ".github/workflows/ci.yml", true},
		{".github/workflows/**", ".github/workflows/sub/x.yml", true},
		{".github/workflows/**", "x/.github/workflows/ci.yml", false},
		{".github/review-tiers.yml", ".github/review-tiers.yml", true},
		{".github/review-tiers.yml", "x/.github/review-tiers.yml", false},
		{"backend/internal/auth/**", "backend/internal/auth/session.go", true},
		{"backend/internal/auth/**", "backend/internal/api/handlers.go", false},
		{"backend/internal/auth", "backend/internal/auth/session.go", true},
		{"backend/internal/auth/", "backend/internal/auth/session.go", true},
		{"internal/auth", "backend/internal/auth/session.go", false},
		{"a/**/b.go", "a/b.go", true},
		{"a/**/b.go", "a/x/y/b.go", true},
		{"a/**/b.go", "a/x/y/c.go", false},
		{"**/auth/**", "backend/auth/x.go", true},
		{"**/auth/**", "auth/x.go", true},
		{"**/auth/**", "backend/authx/x.go", false},
		{"web/*/index.ts", "web/app/index.ts", true},
		{"web/*/index.ts", "web/app/sub/index.ts", false},
		{"**", "anything/at/all", true},
		{"docs", "docs/a.md", true},
		{"docs", "src/docs/a.md", true},
		{"*.test.ts?", "a.test.tsx", true},
		{"[ab].go", "a.go", true},
		{"[ab].go", "c.go", false},
	}
	for _, tc := range tests {
		t.Run(tc.pattern+"|"+tc.path, func(t *testing.T) {
			t.Parallel()
			if got := Match(tc.pattern, tc.path); got != tc.want {
				t.Fatalf("Match(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
			}
		})
	}
}
