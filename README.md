# Wish Git Server

A lightweight SSH-based Git server built with [Charmbracelet's Wish](https://github.com/charmbracelet/wish) and the `git` middleware. This project allows multiple users to interact with Git repositories over SSH, with customizable authentication and automatic commit backup functionality.

---

## ✨ Features

-   🧠 **Public Key-Based Authorization per Repo**

    -   Each Git repo has its own list of authorized SSH public keys.
    -   Public keys are fetched via HTTP from a remote authorization server.
    -   Unauthorized users are denied access to push/fetch/clone.

-   🧳 **Automatic Commit Backup on Push**

    -   Every time a user pushes to a repo, the latest commit is zipped and stored in a local backup directory using the commit SHA as the filename.

-   🗂️ **Repo Listing in SSH**
    -   When a user connects without a Git command, the server lists available repositories and provides cloning instructions.

---

## 🏗️ Project Structure

```txt
.
├── main.go             # Main server logic
├── .repos/             # Where Git repos are stored
├── repo_backups/       # Where commit zip backups are saved
├── .ssh/id_ed25519     # Host SSH private key (generated if missing)
```

---

## 🔐 Authorization Logic

Each repo has its own list of authorized public keys. When a user attempts any Git command (clone, fetch, push), the server:

1. Parses the command to extract the repo name.
2. Makes an HTTP GET request to:

    ```
    http://your-auth-server.local/api/authorized_keys/<repo>
    ```

3. Compares the client's SSH key against the returned public keys.
4. Allows or denies access based on the match.

---

## 🗄️ Push Commit Backup Logic

When a user performs a `git push`, the server:

1. Extracts the latest commit SHA from the repo.
2. Compresses the entire `.git` directory of that repo into a zip file.
3. Saves it at:

    ```
    repo_backups/<repo>/<commit-sha>.zip
    ```

This acts as a simple versioned backup system.

---

## 🧪 Example SSH Usage

### Cloning a Repo

```sh
git clone ssh://<host>:2222/my-repo.git
```

### Creating and Pushing a New Repo

```sh
mkdir my-repo && cd my-repo
git init
git remote add origin ssh://<host>:2222/my-repo.git
git add .
git commit -m "Initial commit"
git push origin master
```

---

## 🛠️ Setup

### 1. Generate SSH Host Key

```sh
ssh-keygen -t ed25519 -C "your_email@example.com"
```

### 2. Run the Server

```sh
go run main.go
```

The server listens on `0.0.0.0:2222`.

---

## 🧩 Configurable Constants

In `main.go`:

```go
const (
  port       = "2222"
  host       = "0.0.0.0"
  repoDir    = ".repos"
  backupDir  = "repo_backups"
  authServer = "http://your-auth-server.local/api/authorized_keys"
)
```

---

## 📁 Directory Overview

-   `.repos/` — All Git repositories live here.
-   `repo_backups/` — Compressed `.zip` backups of each pushed commit.
-   `.ssh/id_ed25519` — SSH private key used to identify the server to clients.

---

## 📌 Dependencies

-   [Charmbracelet Wish](https://pkg.go.dev/github.com/charmbracelet/wish)
-   [Charmbracelet SSH](https://pkg.go.dev/github.com/charmbracelet/ssh)
-   [Charmbracelet log](https://pkg.go.dev/github.com/charmbracelet/log)
-   [Golang SSH](https://pkg.go.dev/golang.org/x/crypto/ssh)
-   `git` (CLI must be installed and in PATH)

---

## 🙏 Credits

Built using:

-   [Charmbracelet Wish](https://github.com/charmbracelet/wish)
-   [Charmbracelet SSH](https://github.com/charmbracelet/ssh)

---

## 🚧 Future Improvements

-   Add user-friendly web UI for repo browsing and key management
-   Webhooks or post-receive Git hooks
-   Fine-grained repo permission control (read vs write)
