package main

import (
	"archive/zip"
	"bytes"
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
	port       = "2222"
	host       = "0.0.0.0"
	repoDir    = "repos"
	backupDir  = "repo_backups"
	authServer = "http://0.0.0.0:3000"
)

type app struct{}

type authorizedKey struct {
	ID  string `json:"id"`
	Key string `json:"key"`
}

func (a app) AuthRepo(repo string, key ssh.PublicKey) git.AccessLevel {
	if isKeyAuthorized(repo, key) {
		return git.ReadWriteAccess
	}
	return git.NoAccess
}

func (a app) Push(repo string, key ssh.PublicKey) {
	log.Info("push", "repo", repo)
	commitSha := getLatestCommitSHA(repo)
	if commitSha != "" {
		backupRepo(repo, commitSha)
	}
}

func (a app) Fetch(repo string, key ssh.PublicKey) {
	log.Info("fetch", "repo", repo)
}

func isKeyAuthorized(repo string, key ssh.PublicKey) bool {
	marshaledKey := string(gossh.MarshalAuthorizedKey(key))
	resp, err := http.Get(fmt.Sprintf("%s/%s", authServer, repo))
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

func getLatestCommitSHA(repo string) string {
	repoPath := filepath.Join(repoDir, repo)
	cmd := exec.Command("git", "--git-dir", repoPath, "rev-parse", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		log.Error("failed to get commit sha", "error", err)
		return ""
	}
	return strings.TrimSpace(string(output))
}

func backupRepo(repo, sha string) {
	src := filepath.Join(repoDir, repo)
	destDir := filepath.Join(backupDir, repo)
	os.MkdirAll(destDir, 0755)

	zipFile := filepath.Join(destDir, fmt.Sprintf("%s.zip", sha))
	f, err := os.Create(zipFile)
	if err != nil {
		log.Error("could not create zip file", "error", err)
		return
	}
	defer f.Close()

	w := zip.NewWriter(f)
	defer w.Close()

	filepath.Walk(src, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		fileData, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := w.Create(relPath)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, bytes.NewReader(fileData))
		return err
	})
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
			gitListMiddleware,
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

func extractRepoFromCommand(cmd []string) string {
	for _, arg := range cmd {
		if strings.Contains(arg, ".git") {
			parts := strings.Split(arg, "/")
			return strings.TrimSuffix(parts[len(parts)-1], ".git")
		}
	}
	return ""
}
