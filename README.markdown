Prolix trims outputs from chatty commands.

This tool acts a bit like an interactive `grep -v`, capturing the output of
a command and filtering out uninteresting lines.

`--ignore-{re, line, substring}` may be used to suppress lines completely.
`--snippet` may be used to rewrite a line, perhaps to trim a log field you're
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
command line, separated by "`--`".

Examples
--------

`prolix --ignore-substring '(spam)' -- mycmd --spamlevel=4`

`cat existing.log | prolix -b "spammy"`



Prolix is written by Gaal Yahas <gaal@forum2.org>.  
Patches/forks are welcome; please see the code at
https://github.com/gaal/prolix-go
