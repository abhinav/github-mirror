// github-mirror mirrors GitHub repositories.
package main

// Ported from github.com/cdown/gh-mirror

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/go-github/github"
	"github.com/jessevdk/go-flags"
)

func listRepositories(username string) ([]*github.Repository, error) {
	c := github.NewClient(nil)
	opts := github.RepositoryListOptions{
		Visibility:  "public",
		Type:        "owner",
		Affiliation: "owner",
	}

	var repos []*github.Repository
	for {
		rs, res, err := c.Repositories.List(username, &opts)
		if err != nil {
			return nil, err
		}

		for _, r := range rs {
			if r.Fork != nil && *r.Fork {
				continue
			}

			if r.Private != nil && *r.Private {
				continue
			}

			repos = append(repos, r)
		}

		if res.NextPage == 0 {
			break
		}
		opts.ListOptions.Page = res.NextPage
	}

	return repos, nil
}

// Synchronizer synchronizes git repositories to a target directory.
type Synchronizer struct {
	TargetDir string
}

func (s *Synchronizer) repoDir(repo *github.Repository) string {
	return filepath.Join(s.TargetDir, filepath.Base(*repo.GitURL))
}

// Sync the given repository.
func (s *Synchronizer) Sync(ctx context.Context, repo *github.Repository) error {
	repoDir := s.repoDir(repo)
	if _, err := os.Stat(repoDir); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to stat %q: %v", repoDir, err)
		}

		if err := git(ctx, "clone", "--mirror", *repo.GitURL, repoDir); err != nil {
			return fmt.Errorf("failed to clone repository %q: %v", *repo.GitURL, err)
		}
	} else if err := git(ctx, "--git-dir", repoDir, "remote", "update"); err != nil {
		return fmt.Errorf("failed to update repository %q: %v", *repo.GitURL, err)
	}

	var desc string
	if repo.Description != nil {
		desc = *repo.Description
	}

	if err := ioutil.WriteFile(filepath.Join(repoDir, "description"), []byte(desc+"\n"), 0666); err != nil {
		log.Printf("Warning: Failed to write description for %q: %v", repoDir, err)
	}

	exportFile := filepath.Join(repoDir, "git-daemon-export-ok")
	if err := ioutil.WriteFile(exportFile, []byte{}, 0666); err != nil {
		log.Printf("Warning: Failed to write export file for %q: %v", repoDir, err)
	}

	return nil
}

func git(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func main() {
	log.SetFlags(0)

	var opts struct {
		Dir     string        `short:"d" value-name:"DIR" default:"." description:"Target directory"`
		Timeout time.Duration `short:"t" long:"timeout" default:"1m" value-name:"DURATION"`
		Args    struct {
			User string `positional-arg-name:"USER" description:"GitHub username"`
		} `positional-args:"yes" required:"yes"`
	}

	_, err := flags.Parse(&opts)
	if err != nil {
		os.Exit(1) // error printed by go-flags
	}

	if newDir, err := filepath.Abs(opts.Dir); err != nil {
		log.Fatalf("error resolving absolute path to %q: %v", opts.Dir, err)
	} else {
		opts.Dir = newDir
	}

	if info, err := os.Stat(opts.Dir); err != nil {
		log.Fatalf("could not stat %q: %v", opts.Dir, err)
	} else if !info.Mode().IsDir() {
		log.Fatalf("%q is not a directory", opts.Dir)
	}

	repos, err := listRepositories(opts.Args.User)
	if err != nil {
		log.Fatalf("failed to fetch repository list: %v", err)
	}

	s := Synchronizer{TargetDir: opts.Dir}
	// TODO: remove repositories present locally that are no longer in the
	// response
	var (
		ctx    = context.Background()
		wg     sync.WaitGroup
		lock   sync.Mutex
		errors []error
	)
	for _, r := range repos {
		wg.Add(1)
		go func(r *github.Repository) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
			defer cancel()

			if err := s.Sync(ctx, r); err != nil {
				lock.Lock()
				errors = append(errors, err)
				lock.Unlock()
			}
		}(r)
	}
	wg.Wait()

	if len(errors) == 0 {
		os.Exit(0)
	}

	log.Println("The following errors occurred:")
	for _, err := range errors {
		log.Println("-", err)
	}
}
