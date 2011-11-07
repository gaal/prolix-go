// Copyright 2011 Google Inc. All rights reserved.

/*
   Prolix trims outputs from chatty commands.

   This tool acts a bit like an interactive grep -v, capturing the output of
   a command and filtering out uninteresting lines.

   --ignore-{re, line, substring} may be used to suppress lines completely.
   --snippet may be used to rewrite a line, perhaps to trim a log field you're
   not interested in on your console. [notyet]

   These flags can be specified more than once.

   While the command runs, hit enter to go into interactive mode: at the
   prompt you can add ignore and snippet directives as you see more spammy
   output the command makes.

   Prolix can also log its own output to a file, so that if you regularly use
   it to debug a server, for example, you can keep somewhat compact logs
   automatically. [notyet]

   Since Prolix knows your command line, it can figure out a profile for
   commands you run, so it'll remember different filters for different
   commands. [notyet]

   You can run existing output via a pipe to prolix and thus filter it, but
   the usual way of invoking it is to pass the command to run on its own
   command line, separated by "--".

   Examples:

   prolix --ignore-substring '(spam)' -- mycmd --spamlevel=4

   cat existing.log | prolix -b "spammy"
*/

package main

import (
	"bufio"
	"exec"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
	"path/filepath"

	"bitbucket.org/binet/go-readline"
)

const versionString = "0.01-go"

var (
	log = flag.String(
		"log", "", "log output file. 'auto' means I pick the filename")
	pipe    = flag.Bool("pipe", false, "pipe mode")
	verbose = flag.Bool("verbose", false, "be verbose")
	version = flag.Bool("version", false, "print version and exit")

	ignoreRe        = make([]string, 0)
	ignoreLine      = make([]string, 0)
	ignoreSubstring = make([]string, 0)
	snippet         = make([]string, 0)
)

var (
	ignoreReVals = make([]*regexp.Regexp, 0)

	linesTotal      = 0
	linesSuppressed = 0

	unaryRe = regexp.MustCompile(`\s*(\S+)\s+(.*)`)

	// The command being run if we're in spawn mode, or nil.
	spawnedProgram *string

	logFile *os.File
)

// The flags package doesn't support multiple flag values, e.g., -i 1 -i 2.
// Handle them ourselves. Must be called before flag.Parse().
func parseMultiValueArgs(args *[]string) {
	grab := func(dest *[]string, i *int) {
		if *i == len(*args) {
			fmt.Errorf("flag needs an argument: %s", (*args)[*i])
			os.Exit(2)
		}

		*dest = append(*dest, (*args)[*i+1])
		copy((*args)[*i:], (*args)[*i+2:])
		*args = (*args)[:len(*args)-2]
		(*i)--
	}
	for i := 0; i < len(*args); i++ {
		// We allow both --foo-bar and --foo_bar.
		switch strings.Replace((*args)[i], "_", "-", -1) {
		case "--ignore-re", "-r":
			grab(&ignoreRe, &i)
		case "--ignore-line", "-i":
			grab(&ignoreLine, &i)
		case "--ignore-substring", "-b":
			grab(&ignoreSubstring, &i)
		//case "--snippet", "-s": grab(&snippet, &i)
		case "--snippet", "-s":
			panic("unimplemented")
		}
	}
}

func importIgnoreRE(pats []string) {
	for _, v := range pats {
		ignoreReVals = append(ignoreReVals, regexp.MustCompile(v))
	}
}

// Opens a file to keep our captured output in. Stdout and Stderr are
// interleaved arbitrarily, though they probably will be complete lines.
// If 'log' is a pathname (contains the path separator), then the location
// will be treated as one. Otherwise, we'll use the temporary dir as the OS
// advises.
//
// The special name "auto" means the command's name (or "prolix" if not known).
// %d expands to the current time.
func openLog() {
	closeLog()
	if log == nil || *log == "" {
		return
	}

	now := time.LocalTime()
	nowString := fmt.Sprintf("%4d%02d%02dT%02d%02d%02d",
		now.Year, now.Month, now.Day, now.Hour, now.Minute, now.Second)
	filename := *log
	if filename == "auto" {
		if spawnedProgram == nil { // Pipe mode.
			filename = "prolix.%d"
		} else {
			filename = *spawnedProgram + ".%d"
		}
	}
	if !strings.Contains(filename, string(os.PathSeparator)) {
		filename = filepath.Join(os.TempDir(), filename)
	}
	filename = strings.Replace(filename, "%d", nowString, -1)

	/*
		    Bizarre! This panics with "nil". How can it?
			if file, err := os.Create(filename); err != nil {
				// TODO(gaal): bufio.NewWriter, but that's not WriterCloser?
				logFile = file
			} else {
				panic(err)
			}
	*/
	logFile, _ = os.Create(filename) // TODO(gaal): Handle errors.
}

func closeLog() {
	if logFile != nil {
		if err := logFile.Close(); err != nil {
			panic(err)
		}
	}
}

func main() {
	parseMultiValueArgs(&os.Args)
	flag.Parse()
	args := flag.Args()
	importIgnoreRE(ignoreRe)
	openLog()

	if *version {
		fmt.Printf("prolix %s\n", versionString)
		os.Exit(0)
	}

	if len(args) == 0 || *pipe {
		prolixPipe()
	} else {
		prolixSpawn(args)
	}

	if *verbose {
		fmt.Printf("Done. Suppressed %d/%d lines.\n",
			linesSuppressed, linesTotal)
	}
}

func prolixSpawn(args []string) {
	if *verbose {
		fmt.Printf("Running: %q\n", args)
	}

	cmd := exec.Command(flag.Args()[0], (flag.Args())[1:]...)
	outReader, err := cmd.StdoutPipe()
	if err != nil {
		panic(err)
	}
	outc := make(chan string)
	errReader, err := cmd.StderrPipe()
	if err != nil {
		panic(err)
	}
	errc := make(chan string)
	err = cmd.Start()
	if err != nil {
		panic(err)
	}

	go readPipe(bufio.NewReader(outReader), outc)
	go readPipe(bufio.NewReader(errReader), errc)

	doneDemux := make(chan string)
	go demux(outc, errc, doneDemux)
	res := <-doneDemux
	if res == "kill" {
		shutdown(cmd.Process)
	}
	err = cmd.Wait()
	// TODO(gaal): exit with child err?
}

func prolixPipe() {
	if *verbose {
		fmt.Println("Running in pipe mode")
	}

	out := bufio.NewReader(os.Stdin)

	// TODO(gaal): refactor to reuse readPipe.
	// NOTE(gaal): I think bufio.ReadLine is problematic because it returns
	// a []byte, and that can fall in the middle of a rune.

	for {
		line, err := out.ReadString('\n')
		if len(line) > 0 {
			// Ugh, I forgot how to initialize a slice?
			wrapped := make([]string, 1)
			wrapped[0] = line
			filterLines(&wrapped)
		}

		if err != nil {
			return
		}
	}

}

// Attempts to shut down a process gracefully.
// There's an obvious race condition here; if we succeed with SIGTERM and
// somebody else gets the same pid in 10 seconds, we'll SIGKILL them.
// Unlikely in most cases? TODO(gaal): maybe add a way for a caller tor
// signal to us that Wait succeeded.
func shutdown(process *os.Process) {
	process.Signal(os.SIGTERM)

	go func() {
		time.Sleep(10e9)
		if _, err := os.FindProcess(process.Pid); err == nil {
			process.Signal(os.SIGKILL)
		}
	}()
}

func readPipe(pipe *bufio.Reader, ch chan<- string) {
	defer close(ch)
	for {
		line, err := pipe.ReadString('\n')
		if len(line) > 0 {
			ch <- line
		}

		if err != nil {
			return
		}
	}
}

func filterLines(lines *[]string) {
	for len(*lines) > 0 {
		line := (*lines)[0]
		linesTotal++
		if okLine(strings.TrimRight(line, "\n")) {
			// TODO(gaal): snippet line
			fmt.Print(line)
			if logFile != nil {
				if _, err := logFile.WriteString(line); err != nil {
					panic(err)
				}
			}
		} else {
			linesSuppressed++
		}
		*lines = (*lines)[1:]
	}
}

func okLine(line string) bool {
	for _, v := range ignoreLine {
		if line == v {
			return false
		}
	}
	for _, v := range ignoreSubstring {
		if strings.Contains(line, v) {
			return false
		}
	}
	for _, v := range ignoreReVals {
		if v.FindStringIndex(line) != nil {
			return false
		}
	}
	return true
}

// Gets additional suppression patterns, etc. from the user.
// TODO(gaal): add completion to go-readline?
func interact(done chan<- string) {
	prompt := "prolix> " // I wonder why I can't make this const.
L:
	for {
		cmd := readline.ReadLine(&prompt)
		if cmd == nil || *cmd == "" {
			break L
		}
		readline.AddHistory(*cmd)
		unary := unaryRe.FindStringSubmatch(*cmd)
		if unary == nil {
			trimmed := strings.TrimSpace(*cmd)
			switch trimmed {
			case "quit":
				done <- "quit"
				return
			case "pats":
				printPats()
			case "help":
				printInteractiveHelp()
			default:
				fmt.Println("Unknown command. Try 'help'.")
			}
		} else {
			switch strings.Replace(unary[1], "_", "-", -1) {
			case "ignore-re":
				ignoreRe = append(ignoreRe, unary[2])
				importIgnoreRE(unary[2:3])
			case "ignore-line":
				ignoreLine = append(ignoreLine, unary[2])
			case "ignore-substring":
				ignoreSubstring = append(ignoreSubstring, unary[2])
			case "snippet":
				panic("unimplemented")
			default:
				fmt.Println("Unknown unary command. Try 'help'.")
			}
		}
	}
	done <- ""
}

func printInteractiveHelp() {
	fmt.Print(
		`ignore-line      - add a full match to ignore
ignore-re        - add an ignore pattern, e.g. ^(FINE|DEBUG)
ignore-substring - add a partial match to ignore
pats             - list ignore patterns
quit             - terminate running program
stats            - print stats
snippet          - add a snippet expression, e.g. s/^(INFO|WARNING|ERROR) //

To keep going, just enter an empty line.
`)
}

func printPats() {
	printList := func(name string, list []string) {
		fmt.Printf(" * %s\n", name)
		for _, v := range list {
			fmt.Println(v)
		}
	}
	printList("ignoreRe", ignoreRe)
	printList("ignoreLine", ignoreLine)
	printList("ignoreSubstring", ignoreSubstring)
	printList("snippet", snippet)
}

func listenKeypress(notify chan int) {
	var buffer [1]byte
	for {
		// TODO(gaal): cook_SetRaw()
		num, _ := os.Stdin.Read(buffer[:])
		if num > 0 {
			// TODO(gaal): cook_SetCooked(), and defer cook_SetCooked() in main.
			notify <- 1
			<-notify
		}
	}
}

func demux(outc, errc <-chan string, done chan<- string) {
	var (
		interacting     = false
		outBuf, errBuf  = make([]string, 0), make([]string, 0)
		keypress        = make(chan int)
		doneInteractive = make(chan string)
	)
	go listenKeypress(keypress)

	for interacting || outc != nil || errc != nil {
		select {
		case newOut, ok := <-outc:
			if ok {
				outBuf = append(outBuf, newOut)
				if !interacting {
					filterLines(&outBuf)
				}
			} else {
				outc = nil
			}
		case newErr, ok := <-errc:
			if ok {
				errBuf = append(errBuf, newErr)
				if !interacting {
					filterLines(&errBuf)
				}
			} else {
				errc = nil
			}
		case <-keypress:
			interacting = true
			go interact(doneInteractive)
		case res := <-doneInteractive:
			if res == "quit" {
				done <- "kill"
				return
			}
			interacting = false
			filterLines(&outBuf)
			filterLines(&errBuf)
			keypress <- 1
		}
	}

	done <- "42"
}
