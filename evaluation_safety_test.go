package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluationPreservesPublishedReport(t *testing.T) {
	t.Setenv("CYPHER_MCP_EVAL_REPORT", "")
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(workingDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	reportPath := filepath.Join(tempDir, "benchmarks", "reports", "BENCHMARKS.md")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
		t.Fatal(err)
	}
	original := []byte("published report must not change during go test\n")
	if err := os.WriteFile(reportPath, original, 0644); err != nil {
		t.Fatal(err)
	}
	if !t.Run("evaluation", TestEvaluateParadigms) {
		t.Fatal("evaluation failed")
	}
	contents, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != string(original) {
		t.Fatal("default evaluation overwrote the published report")
	}
}

func TestEvaluationExplicitReportIsLabeled(t *testing.T) {
	reportPath := filepath.Join(t.TempDir(), "reports", "illustrative.md")
	t.Setenv("CYPHER_MCP_EVAL_REPORT", reportPath)
	if !t.Run("evaluation", TestEvaluateParadigms) {
		t.Fatal("evaluation failed")
	}
	contents, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("explicit report was not generated: %v", err)
	}
	if !strings.Contains(string(contents), "Not an empirical cross-system benchmark") {
		t.Fatal("report does not disclose its illustrative metrics and unmeasured baselines")
	}
}
