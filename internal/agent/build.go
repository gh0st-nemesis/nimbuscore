package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"

	v1 "github.com/gh0st-nemesis/nimbuscore/api/v1"
)

// cloneAuth returns the credential to use for the clone step (e.g. a GitHub
// OAuth token resolved by the dashboard from a connected account), or nil
// for an anonymous clone.
func cloneAuth(token string) transport.AuthMethod {
	if token == "" {
		return nil
	}
	return &githttp.BasicAuth{Username: "x-access-token", Password: token}
}

func buildLogFilePath(logDir, containerID string) string {
	return filepath.Join(logDir, containerID+".build.log")
}

const nodeDockerfileTemplate = `FROM node:21-alpine
WORKDIR /app
COPY . .
RUN npm install --omit=dev
%s
`

// nodeDockerfileWithBuildTemplate is used when package.json declares a
// "build" script (Next.js, Vite, CRA, ...): those frameworks need a build
// step before their start script produces anything runnable, and the build
// step itself commonly needs devDependencies (bundlers, compilers) that
// --omit=dev would have stripped. Install everything, build, then prune dev
// deps back out before shipping.
const nodeDockerfileWithBuildTemplate = `FROM node:21-alpine
WORKDIR /app
COPY . .
RUN npm install
RUN npm run build
RUN npm prune --omit=dev
%s
`

const staticDockerfile = `FROM nginx:alpine
COPY . /usr/share/nginx/html
`

// detectDockerfile synthesizes a Dockerfile for a cloned repo that doesn't
// ship one, so "deploy from git" works without requiring one (Railway/
// Nixpacks-style zero-config detection). Node.js (package.json) and static
// sites (index.html) are the only stacks recognized; anything else fails
// loudly rather than silently producing a broken container.
func detectDockerfile(contextDir string) ([]byte, error) {
	if pkgJSON, err := os.ReadFile(filepath.Join(contextDir, "package.json")); err == nil {
		return nodeDockerfile(pkgJSON), nil
	}
	if _, err := os.Stat(filepath.Join(contextDir, "index.html")); err == nil {
		return []byte(staticDockerfile), nil
	}
	return nil, fmt.Errorf("no supported stack detected (looked for package.json or index.html) — add a Dockerfile to deploy this repo")
}

func nodeDockerfile(pkgJSON []byte) []byte {
	var pkg struct {
		Main    string            `json:"main"`
		Scripts map[string]string `json:"scripts"`
	}
	_ = json.Unmarshal(pkgJSON, &pkg)

	cmd := `CMD ["npm", "start"]`
	if pkg.Scripts["start"] == "" {
		main := pkg.Main
		if main == "" {
			main = "index.js"
		}
		mainJSON, _ := json.Marshal(main)
		cmd = fmt.Sprintf(`CMD ["node", %s]`, mainJSON)
	}

	template := nodeDockerfileTemplate
	if pkg.Scripts["build"] != "" {
		template = nodeDockerfileWithBuildTemplate
	}
	return []byte(fmt.Sprintf(template, cmd))
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

	cloneOpts := &git.CloneOptions{
		URL:           src.GetRepoUrl(),
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		SingleBranch:  true,
		Depth:         1,
		Auth:          cloneAuth(src.GetCloneToken()),
	}
	if _, err := git.PlainCloneContext(ctx, workDir, false, cloneOpts); err != nil {
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

	if src.GetDockerfilePath() == "" {
		if _, err := os.Stat(filepath.Join(dockerfileDir, "Dockerfile")); os.IsNotExist(err) {
			generated, err := detectDockerfile(contextDir)
			if err != nil {
				return "", fmt.Errorf("agent: %w", err)
			}
			if err := os.WriteFile(filepath.Join(dockerfileDir, "Dockerfile"), generated, 0o644); err != nil {
				return "", fmt.Errorf("agent: write generated Dockerfile: %w", err)
			}
			fmt.Fprintf(buildOutput, "nimbuscore: no Dockerfile found, auto-detected stack and generated one:\n%s\n", generated)
		}
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
