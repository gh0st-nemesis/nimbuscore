package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

func buildLogFilePath(logDir, containerID string) string {
	return filepath.Join(logDir, containerID+".build.log")
}

func buildImageFromSource(ctx context.Context, buildkitAddr, workDir, logDir, containerID string, src *v1.BuildSource) (string, error) {
	branch := src.GetBranch()
	if branch == "" {
		branch = "main"
	}

	if err := os.RemoveAll(workDir); err != nil {
		return "", fmt.Errorf("agent: clean build workdir: %w", err)
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return "", fmt.Errorf("agent: create build workdir: %w", err)
	}

	if _, err := git.PlainCloneContext(ctx, workDir, false, &git.CloneOptions{
		URL:           src.GetRepoUrl(),
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		SingleBranch:  true,
		Depth:         1,
	}); err != nil {
		return "", fmt.Errorf("agent: clone %s (%s): %w", src.GetRepoUrl(), branch, err)
	}

	contextDir := workDir
	if src.GetContextPath() != "" {
		contextDir = filepath.Join(workDir, src.GetContextPath())
	}
	dockerfileDir := contextDir
	if src.GetDockerfilePath() != "" {
		dockerfileDir = filepath.Join(workDir, filepath.Dir(src.GetDockerfilePath()))
	}

	imageTag := "docker.io/library/" + containerID + ":latest"

	var buildOutput io.Writer = io.Discard
	if logDir != "" {
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			return "", fmt.Errorf("agent: create log dir: %w", err)
		}
		logFile, err := os.Create(buildLogFilePath(logDir, containerID))
		if err != nil {
			return "", fmt.Errorf("agent: create build log: %w", err)
		}
		defer logFile.Close()
		buildOutput = logFile
	}

	args := []string{
		"--addr", buildkitAddr,
		"build",
		"--frontend", "dockerfile.v0",
		"--local", "context=" + contextDir,
		"--local", "dockerfile=" + dockerfileDir,
		"--output", "type=image,name=" + imageTag + ",unpack=true",
	}
	cmd := exec.CommandContext(ctx, "buildctl", args...)
	cmd.Stdout = buildOutput
	cmd.Stderr = buildOutput
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("agent: buildctl build failed (see build logs): %w", err)
	}

	return imageTag, nil
}
