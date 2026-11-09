                                                                                     
                                                                                    
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var repoDir = flag.String("repo", "../frostbit", "frostbit 仓库目录（绝对路径或相对 dex 根）")

func main() {
	flag.Parse()
	src, err := os.Getwd()
	check(err, "getwd")
	dst := *repoDir
	if !filepath.IsAbs(dst) {
		dst = filepath.Join(src, dst)
	}
	if _, err := os.Stat(filepath.Join(dst, ".git")); err != nil {
		fatal(fmt.Sprintf("frostbit 仓库不存在：%s（用 -repo 指定）", dst))
	}

	for _, name := range []string{"README.md", "README.en.md"} {
		copyFile(filepath.Join(src, "bin", name), filepath.Join(dst, name))
	}

	run(dst, "git", "add", "README.md", "README.en.md")

	if exec.Command("git", "-C", dst, "diff", "--cached", "--quiet").Run() == nil {
		fmt.Println("No README changes to commit.")
		return
	}

	run(dst, "git", "commit", "-m", "docs: update FrostBit README (zh/en)")
	run(dst, "git", "push")
	fmt.Println("Pushed to frostbit ✓")
}

func copyFile(src, dst string) {
	in, err := os.Open(src)
	check(err, "open "+src)
	defer in.Close()
	out, err := os.Create(dst)
	check(err, "create "+dst)
	defer out.Close()
	_, err = io.Copy(out, in)
	check(err, "copy "+src)
	fmt.Printf("copied %s → %s\n", filepath.Base(src), dst)
}

func run(dir, name string, args ...string) {
	fmt.Printf("[%s] %s %s\n", dir, name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	check(cmd.Run(), name)
}

func check(err error, what string) {
	if err != nil {
		fatal(what + ": " + err.Error())
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
