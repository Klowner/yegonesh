package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Command struct {
	Name  string
	Calls uint64
}

type Commands []*Command

func (s Commands) Len() int {
	return len(s)
}

func (s Commands) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

type ByScore struct{ Commands }

func (s ByScore) Less(i, j int) bool {
	return s.Commands[i].Calls < s.Commands[j].Calls
}

func check(e error) {
	if e != nil {
		panic(e)
	}
}

func fetchExecutables(dir string) <-chan string {
	out := make(chan string, 1)

	go func() {
		files, _ := ioutil.ReadDir(dir)
		for _, f := range files {
			if f.Mode()&0111 != 0 {
				out <- f.Name()
			}
		}
		close(out)
	}()

	return out
}

// get all the items specified by the components of $PATH
func scanPath(path string) <-chan string {
	var group sync.WaitGroup
	var commands []string

	out := make(chan string)

	dirs := strings.Split(path, ":")

	for _, dir := range dirs {
		group.Add(1)

		go func(dir string) {
			for item := range fetchExecutables(dir) {
				commands = append(commands, item)
			}
			group.Done()
		}(dir)
	}

	go func() {
		group.Wait()
		sort.Strings(commands)
		for _, cmd := range commands {
			out <- cmd
		}
		close(out)
	}()

	return out
}

func readHistory(path string) Commands {
	var history Commands

	f, err := os.Open(path)

	if err != nil {
		return nil //history
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		s := strings.Split(line, "\t")
		calls, err := strconv.ParseInt(s[0], 10, 64)
		check(err)
		history = append(history, &Command{s[1], uint64(calls)})
	}

	f.Close()
	sort.Sort(sort.Reverse(ByScore{history}))

	return history
}

func writeHistory(path string, commands Commands, lastCommand string) {
	f, err := os.Create(path)
	check(err)

	write := func(command *Command) {
		fmt.Fprintf(f, "%d\t%s\n", command.Calls, command.Name)
	}

	for _, command := range commands {
		if command.Name == lastCommand {
			command.Calls += 1
			lastCommand = ""
		}
		write(command)
	}

	if lastCommand != "" {
		write(&Command{lastCommand, 1})
	}
	f.Close()
}

func multiplexMenuStreams(history <-chan string, commands <-chan string) <-chan string {
	// drain history first while placing items into a map so they can
	// be omitted from the commands channel

	out := make(chan string)
	seen := make(map[string]bool)

	go func() {
		for cmd := range history {
			seen[cmd] = true
			out <- cmd
		}

		for cmd := range commands {
			if _, ok := seen[cmd]; !ok {
				out <- cmd
			}
		}
		close(out)
	}()

	return out
}

func historyNameStream(commands Commands) <-chan string {
	out := make(chan string)
	go func() {
		for _, command := range commands {
			out <- command.Name
		}
		close(out)
	}()
	return out
}

func runDmenu(items <-chan string) string {
	args := dmenuArgs()
	c := exec.Command("dmenu", args...)

	out := &bytes.Buffer{}
	c.Stdout = out
	in, err := c.StdinPipe()
	check(err)

	c.Start()
	for cmd := range items {
		fmt.Fprintf(in, "%s\n", cmd)
	}
	in.Close()

	c.Wait()

	// return the command submitted to dmenu
	return strings.TrimSpace(out.String())
}

func dmenuArgs() []string {
	args := os.Args[1:]

	for i, val := range args {
		if val == "--" {
			return args[i+1:]
		}
	}
	return nil
}

func launchCommand(command string) *exec.Cmd {
	split := strings.SplitN(command, " ", 2)
	command = split[0]
	args := split[1:]
	path, err := exec.LookPath(command)
	check(err)
	cmd := exec.Command(path, args...)
	cmd.Start()
	return cmd
}

func getConfigDir() string {
	home := os.Getenv("XDG_CONFIG_HOME")
	var configdir string

	if len(home) > 0 {
		configdir = path.Join(home, "/yegonesh")
	} else {
		configdir = path.Join(os.Getenv("HOME"), "/.local/config/yegonesh")
	}

	os.MkdirAll(configdir, 0700)

	return configdir
}

func main() {
	os.Chdir(os.Getenv("HOME"))
	executables := scanPath(os.Getenv("PATH"))
	configdir := getConfigDir()
	historyPath := path.Join(configdir, "history.tsv")
	history := readHistory(historyPath)

	cmd := runDmenu(
		multiplexMenuStreams(
			historyNameStream(history),
			executables),
	)

	if len(cmd) > 0 {
		// launch the requested process
		launchCommand(cmd)

		// update the launch history
		writeHistory(historyPath, history, cmd)
	}
}
