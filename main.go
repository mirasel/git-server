package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/git"
	"github.com/charmbracelet/wish/logging"
	gossh "golang.org/x/crypto/ssh"
)

var (
	repoNameRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)
	repoMutex     = sync.Mutex{}
	config        = loadConfig()
)

type app struct {
	config Config
}

type authorizedKey struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

func (a app) AuthRepo(repo string, key ssh.PublicKey) git.AccessLevel {
	if !isValidRepoName(repo) {
		log.Warn("Invalid repository name", "repo", repo)
		return git.NoAccess
	}

	if isKeyAuthorized(repo, key) {
		repoPath := filepath.Join(config.RepoDir, repo)
		if _, err := os.Stat(repoPath); os.IsNotExist(err) {
			log.Info("Creating new repository", "repo", repo)

			err := createBareRepoWithHook(repo)
			if err != nil {
				log.Error("Repository creation failed", "repo", repo)
				return git.NoAccess
			}
		}
		return git.ReadWriteAccess
	}
	return git.NoAccess
}

func (a app) Push(repo string, key ssh.PublicKey) {
	log.Info("push", "repo", repo)
}

func (a app) Fetch(repo string, key ssh.PublicKey) {
	log.Info("fetch", "repo", repo)
}

func (a app) Pull(repo string, key ssh.PublicKey) {
	log.Info("pull", "repo", repo)
}

func isValidRepoName(repo string) bool {
	if len(repo) == 0 || len(repo) > 100 {
		return false
	}
	if strings.Contains(repo, "..") || strings.Contains(repo, "/") {
		return false
	}
	return repoNameRegex.MatchString(repo)
}

func isKeyAuthorized(repo string, key ssh.PublicKey) bool {
	client := &http.Client{Timeout: config.HTTPTimeout}
	marshaledKey := string(gossh.MarshalAuthorizedKey(key))

	resp, err := client.Get(fmt.Sprintf("%s/%s", config.InternalServer, repo))
	if err != nil {
		log.Error("Authorization check failed")
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error("Failed to read response")
		return false
	}

	var authKeys []authorizedKey
	if err := json.Unmarshal(data, &authKeys); err != nil {
		log.Error("Invalid response format")
		return false
	}

	for _, authKey := range authKeys {
		keyPart := strings.Split(authKey.Key, " ")
		keyWithoutUserIdentity := strings.Join(keyPart[0:len(keyPart)-1], " ")
		if strings.TrimSpace(keyWithoutUserIdentity) == strings.TrimSpace(marshaledKey) {
			return true
		}
	}
	return false
}

func gitListMiddleware(next ssh.Handler) ssh.Handler {
	return func(sess ssh.Session) {
		if len(sess.Command()) != 0 {
			next(sess)
			return
		}
		dest, err := os.ReadDir(config.RepoDir)
		if err != nil && err != fs.ErrNotExist {
			log.Error("invalid repository", "error", err)
		}
		if len(dest) > 0 {
			fmt.Fprintf(sess, "\n### Repo Menu ###\n\n")
		}
		for _, dir := range dest {
			fmt.Fprintf(sess, "• %s\n", dir.Name())
			fmt.Fprintf(sess, "git clone ssh://%s/%s\n", net.JoinHostPort(config.Host, config.Port), dir.Name())
		}
		fmt.Fprintf(sess, "\n\n### Add some repos! ###\n\n")
		fmt.Fprintf(sess, "> cd some_repo\n")
		fmt.Fprintf(sess, "> git remote add wish_test ssh://%s/some_repo\n", net.JoinHostPort(config.Host, config.Port))
		fmt.Fprintf(sess, "> git push wish_test\n\n\n")
		next(sess)
	}
}

func createBareRepoWithHook(repoName string) error {
	repoMutex.Lock()
	defer repoMutex.Unlock()

	repoPath := filepath.Join(config.RepoDir, repoName)

	if _, err := os.Stat(repoPath); !os.IsNotExist(err) {
		return nil
	}

	if err := os.MkdirAll(repoPath, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	cmd := exec.Command("git", "init", "--bare", repoPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to initialize repository: %w", err)
	}

	return createPostReceiveHook(repoPath, repoName)
}

func createPostReceiveHook(repoPath, repoName string) error {
	hookPath := filepath.Join(repoPath, "hooks", "post-receive")
	hookScript := fmt.Sprintf(`#!/bin/bash
set -e

BACKUP_ROOT="%s"
REPO_NAME="%s"
UPLOAD_URL="%s/upload"

while IFS=' ' read -r oldrev newrev refname; do
	if [ "$newrev" = "0000000000000000000000000000000000000000" ]; then
		continue
	fi
	
	ZIP_NAME="${newrev}.zip"
	DEST_DIR="$BACKUP_ROOT/$REPO_NAME"
	DEST_PATH="$DEST_DIR/$ZIP_NAME"
	
	mkdir -p "$DEST_DIR"
	git archive "$newrev" --format=zip -o "$DEST_PATH"
	
	curl -X POST "$UPLOAD_URL" \
		-F "repo=$REPO_NAME" \
		-F "commit=$newrev" \
		-F "file=@$DEST_PATH" \
		--max-time 30 \
		--retry 3 \
		--fail --silent --show-error || echo "Upload failed for $newrev"
done
`, filepath.Join("..", "..", config.BackupDir), repoName, config.InternalServer)

	return os.WriteFile(hookPath, []byte(hookScript), 0755)
}

func main() {
	a := app{config: config}

	s, err := wish.NewServer(
		wish.WithAddress(net.JoinHostPort(config.Host, config.Port)),
		wish.WithHostKeyPath(config.SSHKeyPath),
		ssh.PublicKeyAuth(func(ctx ssh.Context, key ssh.PublicKey) bool {
			return true
		}),
		wish.WithMiddleware(
			git.Middleware(config.RepoDir, a),
			// gitListMiddleware, // uncomment to see SSH interface, (basically available repos and clone instructions)
			logging.Middleware(),
		),
	)
	if err != nil {
		log.Fatal("could not start server", "error", err)
	}
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	log.Info("Starting SSH server", "host", config.Host, "port", config.Port)
	go func() {
		if err := s.ListenAndServe(); err != nil && !errors.Is(err, ssh.ErrServerClosed) {
			log.Error("could not start server", "error", err)
			done <- nil
		}
	}()
	<-done
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.Shutdown(ctx)
}
