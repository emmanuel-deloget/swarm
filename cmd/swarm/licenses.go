package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"

	"github.com/emmanuel-deloget/swarm/internal/licenses"
)

// swarm licenses answers for what is inside the binary. A list by default,
// because thirty-four licence texts is not something anyone reads by
// accident; a name to see one in full; -all to write the lot somewhere.
//
// A command rather than a screen in the TUI: this is text to pipe into a
// pager, grep for a module, or redirect into a file when someone asks what a
// deployment contains. None of that wants a viewport.
func cmdLicenses(args []string) error {
	fs := newFlagSet("licenses")
	all := fs.Bool("all", false, "print every licence in full")
	if err := parseArgs(fs, args, -1); err != nil {
		return err
	}
	query := strings.Join(fs.Args(), " ")

	notices, err := licenses.Find(query)
	if err != nil {
		return err
	}
	if len(notices) == 0 {
		return fmt.Errorf("nothing bundled matches %q; `swarm licenses` lists what is here", query)
	}

	// A name was asked for, or every text was: print the texts.
	if query != "" || *all {
		for i, n := range notices {
			if i > 0 {
				fmt.Println()
			}
			printNotice(n)
		}
		return nil
	}

	printList(notices)
	return nil
}

// printNotice writes one work and its terms, under a rule wide enough to be
// found by eye when scrolling through all of them.
func printNotice(n licenses.Notice) {
	fmt.Println(strings.Repeat("─", 72))
	fmt.Println(n.Title())
	if n.About != "" {
		fmt.Println(n.About)
	}
	switch {
	case n.File == "":
		fmt.Println(n.Kind())
	case strings.HasPrefix(n.File, "("):
		// No licence file upstream; what follows is the note written for it.
		fmt.Printf("%s — no licence file in the module\n", n.Kind())
	default:
		fmt.Printf("%s, as %s upstream\n", n.Kind(), n.File)
	}
	fmt.Println(strings.Repeat("─", 72))
	fmt.Println()
	fmt.Println(strings.TrimRight(n.Text, "\n"))
}

func printList(notices []licenses.Notice) {
	var self, bundled, mods []licenses.Notice
	for _, n := range notices {
		switch {
		case n.Self:
			self = append(self, n)
		case n.Bundled:
			bundled = append(bundled, n)
		default:
			mods = append(mods, n)
		}
	}

	// Two shapes, because a licence list is read down the column the licence
	// names are in, and that column has to be straight.
	//
	// Wide enough for the longest title: name first, licence aligned after it.
	// Not wide enough: licence first, name after. Capping the name column
	// instead was the first attempt and it produced the worst of both — the
	// handful of modules with no tagged release carry a pseudo-version of date
	// and commit, they overran the cap, and each one shoved its licence out of
	// column. Putting the aligned column on the left cannot be pushed out of
	// line by anything, however long the name that follows it.
	title, kind := 0, 0
	for _, n := range notices {
		if w := len(n.Title()); w > title {
			title = w
		}
		if w := len(n.Kind()); w > kind {
			kind = w
		}
	}
	nameFirst := 2+title+2+kind <= termWidth()

	row := func(n licenses.Notice) {
		if nameFirst {
			fmt.Printf("  %-*s  %s\n", title, n.Title(), n.Kind())
		} else {
			fmt.Printf("  %-*s  %s\n", kind, n.Kind(), n.Title())
		}
		if n.About != "" {
			// Under its row rather than in either column: a sentence aligned
			// to a column sized for names is a line past the right edge.
			fmt.Printf("    %s\n", n.About)
		}
	}

	for _, n := range self {
		fmt.Printf("%s  %s\n", n.Title(), n.Kind())
	}

	fmt.Println("\nIt carries these works inside its binary.")
	if len(bundled) > 0 {
		fmt.Println("\nbundled")
		for _, n := range bundled {
			row(n)
		}
	}
	if len(mods) > 0 {
		fmt.Printf("\nmodules (%d)\n", len(mods))
		for _, n := range mods {
			row(n)
		}
	}
	fmt.Println("\nA licence named above is what the text says, not a legal opinion;")
	fmt.Println("`swarm licenses <name>` prints one in full, `-all` prints every one.")
}

// widestKind is how much room the right-hand column needs.
func widestKind(notices []licenses.Notice) int {
	w := 0
	for _, n := range notices {
		if k := len(n.Kind()); k > w {
			w = k
		}
	}
	return w
}

// termWidth is the width of the terminal, or eighty when there is no terminal
// to ask — a pipe, a file, a CI log. Eighty rather than something generous:
// output that is being captured is output someone may read anywhere.
func termWidth() int {
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		return w
	}
	return 80
}
