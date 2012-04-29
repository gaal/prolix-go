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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/bobappleyard/readline"
	"github.com/gaal/go-options/options"
)

const versionString = "0.03-go"

const timestampFormat = "20060102T150405" // yyyyMMddThhmmss in localtime.

const optionSpec = `
prolix [PROLIX OPTIONS] -- my_command [SPAWNED COMMAND OPTIONS]
Runs "my_command", filtering its standard output and error.
Hit ENTER while the command is running to add new filters. "help" in that
mode will show you a list of available commands.
--
l,log= log output to a file. The special file "auto" let me pick a name.
p,pipe force prolix into pipe mode (not interactive).
v,verbose print some information about what prolix is doing.
r,ignore-re= ignore lines matching this regexp.
n,ignore-line= ignore lines equal to this entirely.
b,ignore-substring= ignore lines containing this substring.
s,snippet= trim the line with this substitution. e.g., s/DEBUG|INFO//.
`

var (
	log     string
	pipe    = false
	verbose = false

	ignoreRe        = make([]string, 0)
	ignoreLine      = make([]string, 0)
	ignoreSubstring = make([]string, 0)
	snippet         = make([]string, 0)
)

var (
	ignoreReVals     = make([]*regexp.Regexp, 0)
	substitutionVals = make([]Substitution, 0)

	linesTotal      = 0
	linesSuppressed = 0

	unaryRe = regexp.MustCompile(`\s*(\S+)\s+(.+)`)

	// The command being run if we're in spawn mode, or nil.
	spawnedProgram *string

	logFile *os.File
)

type Substitution struct {
	search  *regexp.Regexp
	replace string
	global  bool
}

// A go-options compatible parser.
func myParse(s *options.OptionSpec, option string, value *string) {
	if value == nil {
		switch s.GetCanonical(option) {
		case "pipe":
			pipe = true
		case "verbose":
			verbose = true
		case "version":
			{
				fmt.Printf("prolix %s\n", versionString)
				os.Exit(0)
			}
		default:
			s.PrintUsageAndExit("Unknown option: " + option)
		}
	} else {
		switch s.GetCanonical(option) {
		case "log":
			log = *value
		case "ignore-re":
			ignoreRe = append(ignoreRe, *value)
		case "ignore-line":
			ignoreLine = append(ignoreLine, *value)
		case "ignore-substring":
			ignoreSubstring = append(ignoreSubstring, *value)
		case "snippet":
			snippet = append(snippet, *value)
		default:
			s.PrintUsageAndExit("Unknown option: " + option)
		}
	}
}

func importIgnoreRE(pats []string) {
	for _, v := range pats {
		ignoreReVals = append(ignoreReVals, regexp.MustCompile(v))
	}
}

func importSnippet(subsitutions []string) (ok bool) {
	for _, sub := range subsitutions {
		if len(sub) < 4 {
			fmt.Fprint(os.Stderr, "invalid substitution: ", sub)
			return
		}
		delim := sub[1:2]
		// TODO(gaal): paired delimiters, e.g., s{foo}{bar}
		parse := regexp.MustCompile("^s" + delim +
			`((?:\\.|[^` + regexp.QuoteMeta(delim) + `])*)` + delim +
			`((?:\\.|[^` + regexp.QuoteMeta(delim) + `])*)` + delim + "([ig])*$")
		parsedSub := parse.FindStringSubmatch(sub)
		if len(parsedSub) != 4 {
			fmt.Fprint(os.Stderr, "invalid substitution: ", sub)
			return
		}
		global := strings.Contains(parsedSub[3], "g")
		ignoreCase := strings.Contains(parsedSub[3], "i")
		pat := parsedSub[1]
		if ignoreCase {
			pat = "(?i)" + pat
		}
		if search, err := regexp.Compile(pat); err == nil {
			substitutionVals = append(substitutionVals, Substitution{search, parsedSub[2], global})
		} else {
			fmt.Fprint(os.Stderr, "invalid substitution: ", sub)
			return
		}
	}
	return true
}

// regexp does not have a ReplaceFirst. Go figure.
func ReplaceFirst(search *regexp.Regexp, replace, input string) string {
	if m := search.FindStringSubmatchIndex(input); m != nil {
		output := make([]byte, m[0])
		copy(output, input[0:m[0]])
		output = search.ExpandString(output, replace, input, m)
		if m[1] < len(input) {
			return string(output) + input[m[1]:]
		}
		return string(output)
	}
	return input
}

func substitute(sub Substitution, input string) string {
	if sub.global {
		return sub.search.ReplaceAllString(input, sub.replace)
	}
	return ReplaceFirst(sub.search, sub.replace, input)
}

func substituteAll(input string) (out string) {
	out = input
	for _, sub := range substitutionVals {
		out = substitute(sub, out)
	}
	return
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
	if log == "" {
		return
	}

	nowString := time.Now().Format(timestampFormat)
	filename := log
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

var completionWords = []string{
	"ignore-line",
	"ignore-re",
	"ignore-substring",
	"snippet",

	"pats",
	"quit",
	"stats",
	"help"}

func interactiveCompletion(text, ctx string) (out []string) {
	for _, word := range completionWords {
		if strings.HasPrefix(word, text) {
			out = append(out, word)
		}
	}
	return
}

func main() {
	readline.Completer = interactiveCompletion
	spec := options.NewOptions(optionSpec).SetParseCallback(myParse)
	opt := spec.Parse(os.Args[1:])
	args := opt.Leftover
	importIgnoreRE(ignoreRe)
	if !importSnippet(snippet) {
		os.Exit(1)
	}
	openLog()

	if len(args) == 0 || pipe {
		prolixPipe()
	} else {
		prolixSpawn(args)
	}

	if verbose {
		fmt.Printf("Done. Suppressed %d/%d lines.\n",
			linesSuppressed, linesTotal)
	}
}

func prolixSpawn(args []string) {
	if verbose {
		fmt.Printf("Running: %q\n", args)
	}

	cmd := exec.Command(args[0], args[1:]...)
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
	if verbose {
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
	process.Signal(syscall.SIGTERM)

	go func() {
		time.Sleep(10e9)
		if _, err := os.FindProcess(process.Pid); err == nil {
			process.Signal(syscall.SIGKILL) // == process.Kill()
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
		trimmed := strings.TrimRight(line, "\n")
		if okLine(trimmed) {
			trimmed = substituteAll(trimmed)
			if strings.HasSuffix(line, "\n") {
				trimmed = trimmed + "\n"
			}
			fmt.Print(trimmed)
			if logFile != nil {
				if _, err := logFile.WriteString(trimmed); err != nil {
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
func interact(done chan<- string) {
	const prompt = "prolix> "
L:
	for {
		cmd := readline.String(prompt)
		if cmd == "" {
			break L
		}
		readline.AddHistory(cmd)
		unary := unaryRe.FindStringSubmatch(cmd)
		if unary == nil {
			trimmed := strings.TrimSpace(cmd)
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
				if importSnippet(unary[2:3]) {
					snippet = append(snippet, unary[2])
				}
			default:
				fmt.Println("Unknown unary command. Try 'help'.")
			}
		}
	}
	done <- ""
}

func printInteractiveHelp() {
	fmt.Print(`
ignore-line      - add a full match to ignore
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
			if verbose {
				fmt.Println(
					`Press ENTER to go back, or enter "help" for a list of commands.`)
			}
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
