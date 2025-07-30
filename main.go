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
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/git"
	"github.com/charmbracelet/wish/logging"
	gossh "golang.org/x/crypto/ssh"
)

const (
	port           = "2222"
	host           = "0.0.0.0"
	repoDir        = "repos"
	backupDir      = "repo_backups"
	internalServer = "http://0.0.0.0:3000"
)

type app struct{}

type authorizedKey struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

func (a app) AuthRepo(repo string, key ssh.PublicKey) git.AccessLevel {
	if isKeyAuthorized(repo, key) {
		repoPath := filepath.Join(repoDir, repo)
		// Auto-create bare repo if not exists
		if _, err := os.Stat(repoPath); os.IsNotExist(err) {
			log.Info("Repo not found. Creating new bare repo...", "repo", repo)

			err := createBareRepoWithHook(repo)
			if err != nil {
				log.Error("failed to create repo", "repo", repo, "error", err)
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

func isKeyAuthorized(repo string, key ssh.PublicKey) bool {
	marshaledKey := string(gossh.MarshalAuthorizedKey(key))
	resp, err := http.Get(fmt.Sprintf("%s/%s", internalServer, repo))
	if err != nil {
		log.Error("failed to fetch authorized keys", "error", err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return false
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error("failed to read authorized keys", "error", err)
		return false
	}

	authKeys := make([]authorizedKey, 0)
	err = json.Unmarshal(data, &authKeys)
	if err != nil {
		log.Error("failed to convert into json key", "error", err)
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
		dest, err := os.ReadDir(repoDir)
		if err != nil && err != fs.ErrNotExist {
			log.Error("invalid repository", "error", err)
		}
		if len(dest) > 0 {
			fmt.Fprintf(sess, "\n### Repo Menu ###\n\n")
		}
		for _, dir := range dest {
			fmt.Fprintf(sess, "• %s\n", dir.Name())
			fmt.Fprintf(sess, "git clone ssh://%s/%s\n", net.JoinHostPort(host, port), dir.Name())
		}
		fmt.Fprintf(sess, "\n\n### Add some repos! ###\n\n")
		fmt.Fprintf(sess, "> cd some_repo\n")
		fmt.Fprintf(sess, "> git remote add wish_test ssh://%s/some_repo\n", net.JoinHostPort(host, port))
		fmt.Fprintf(sess, "> git push wish_test\n\n\n")
		next(sess)
	}
}

func createBareRepoWithHook(repoName string) error {
	repoPath := filepath.Join(repoDir, repoName)

	// Create the bare repo
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		return err
	}
	cmd := exec.Command("git", "init", "--bare", repoPath)
	if err := cmd.Run(); err != nil {
		return err
	}

	// Write post-receive hook
	hookPath := filepath.Join(repoPath, "hooks", "post-receive")
	hookScript := `#!/bin/bash
	BACKUP_ROOT="` + "../../" + backupDir + `"
	REPO_NAME=$(basename "$(pwd)" .git)
	UPLOAD_URL="` + internalServer + `/upload"

	while read oldrev newrev refname; do
		ZIP_NAME="$newrev.zip"
		DEST_DIR="$BACKUP_ROOT/$REPO_NAME"
		DEST_PATH="$DEST_DIR/$ZIP_NAME"

		mkdir -p "$DEST_DIR"
		git archive "$newrev" --format zip -o "$DEST_PATH"

		# Upload via curl as multipart/form-data
		curl -X POST "$UPLOAD_URL" \
			-F "repo=$REPO_NAME" \
			-F "commit=$newrev" \
			-F "file=@$DEST_PATH" \
			--fail --silent --show-error
	done
	`

	if err := os.WriteFile(hookPath, []byte(hookScript), 0755); err != nil {
		return err
	}
	return nil
}

func main() {
	a := app{}

	s, err := wish.NewServer(
		wish.WithAddress(net.JoinHostPort(host, port)),
		wish.WithHostKeyPath(".ssh/id_ed25519"),
		ssh.PublicKeyAuth(func(ctx ssh.Context, key ssh.PublicKey) bool {
			return key != nil
		}),
		wish.WithMiddleware(
			git.Middleware(repoDir, a),
			// gitListMiddleware,
			logging.Middleware(),
		),
	)
	if err != nil {
		log.Fatal("could not start server", "error", err)
	}
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	log.Info("Starting SSH server", "host", host, "port", port)
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
