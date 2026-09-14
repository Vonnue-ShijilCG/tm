package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"
)

func main() {
	root, bin := os.Args[1], os.Args[2]
	cmd := exec.Command(bin)
	cmd.Dir = root
	env := append(os.Environ(), "HOME="+root, "SHELL=/bin/sh", "TERM=xterm-256color")
	if len(os.Args) > 3 && os.Args[3] == "full" {
		env = append(env, "XDG_CONFIG_HOME="+root+"/config", "PS1=$ ")
	}
	cmd.Env = env
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 100, Rows: 30})
	if err != nil {
		fmt.Println("start:", err)
		return
	}
	go func() { time.Sleep(2500 * time.Millisecond); _ = cmd.Process.Kill() }()
	buf := make([]byte, 65536)
	total := 0
	for {
		n, err := tty.Read(buf)
		if n > 0 {
			if total == 0 {
				fmt.Printf("first: %q\n", string(buf[:min(n, 80)]))
			}
			total += n
		}
		if err != nil {
			break
		}
	}
	fmt.Println("total:", total)
}
