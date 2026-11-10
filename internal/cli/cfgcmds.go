package cli

import (
	"fmt"
	"os"
	"strings"

	"necorouter/internal/config"
)

// Commands for working with configuration files. Some commands are available right away
// (write file, write terminal, show files config, show config file, copy file ... startup-config),
// the rest are enabled by the feature command: feature config-features.
// Before the feature is enabled, its commands are visible neither in ? nor in completion.

func featOn(name string) func(*Session) bool {
	return func(s *Session) bool { return s.cfg().Features[name] }
}

// serviceNode: service password-encryption (a vanilla command, always visible).
// Passwords are already stored as hashes here, so the mode only marks the configuration.
func serviceNode() *Node {
	return K("service", "System services",
		K("password-encryption", "Encrypt passwords stored in the configuration (passwords are always stored as hashes here)").
			R(func(s *Session, _ []string) error { s.cfg().PwEncrypt = true; return nil }).
			N(func(s *Session, _ []string) error { s.cfg().PwEncrypt = false; return nil }))
}

// featureNode: feature NAME / no feature NAME — enabling non-vanilla functions. Hidden from hints
// entirely, so that nothing is revealed before enabling; after the full name is entered, the feature's commands open.
func featureNode() *Node {
	set := func(name string, on bool) Handler {
		return func(s *Session, _ []string) error {
			c := s.cfg()
			if c.Features == nil {
				c.Features = map[string]bool{}
			}
			c.Features[name] = on
			s.journal("feature %s %s", name, map[bool]string{true: "on", false: "off"}[on])
			return nil
		}
	}
	feat := func(name, help string) *Node {
		return K(name, help).R(set(name, true)).N(set(name, false))
	}
	return K("feature", "Enable an optional feature",
		feat("config-features", "Configuration files: startup-config history and autosave, copying between files and into running-config, deleting files, diff, operations journal"),
	)
}

// showFeaturesNode: show features — always visible, like the other show commands.
func showFeaturesNode() *Node {
	return K("features", "Show the optional features and their state").R(func(s *Session, _ []string) error {
		for _, f := range config.Features {
			st := "off"
			if s.cfg().Features[f] {
				st = "on"
			}
			s.Printf("%-16s %s\n", f, st)
		}
		return nil
	})
}

// historyCfgNodes: configuration mode commands available with feature config-features.
func historyCfgNodes() []*Node {
	on := featOn("config-features")
	startup := K("startup-history", "Keep the last N copies of startup-config when it is overwritten (default 10)",
		K("keep", "Number of copies to keep",
			A(KInt, "N", "Copies (1-64)").Rng(1, maxKeep).R(func(s *Session, v []string) error {
				fmt.Sscan(v[0], &s.cfg().StartupKeep)
				s.journal("startup-history keep %d", s.cfg().StartupKeep)
				return nil
			}))).When(on)
	setKeep := func(s *Session, v []string) error {
		fmt.Sscan(v[len(v)-1], &s.cfg().AutosaveKeep)
		return nil
	}
	autosave := K("autosave", "Save running-config to the autosave history (off by default; enabled by autosave interval); only when it differs from startup-config",
		K("interval", "Autosave period in minutes (default 5; no autosave turns it off)",
			A(KInt, "MIN", "Minutes (1-1440)",
				K("keep", "Number of autosave copies to keep (default 10)",
					A(KInt, "N", "Copies (1-64)").Rng(1, maxKeep).R(func(s *Session, v []string) error {
						fmt.Sscan(v[0], &s.cfg().AutosaveMin)
						s.cfg().AutosaveOn = true
						err := setKeep(s, v)
						s.journal("autosave interval %d keep %d", s.cfg().AutosaveMin, s.cfg().AutosaveKeep)
						return err
					}))).Rng(1, 1440).R(func(s *Session, v []string) error {
				fmt.Sscan(v[0], &s.cfg().AutosaveMin)
				s.cfg().AutosaveOn = true
				s.journal("autosave interval %d", s.cfg().AutosaveMin)
				return nil
			})),
		K("keep", "Number of autosave copies to keep (default 10)",
			A(KInt, "N", "Copies (1-64)").Rng(1, maxKeep).R(func(s *Session, v []string) error {
				err := setKeep(s, v)
				s.journal("autosave keep %d", s.cfg().AutosaveKeep)
				return err
			})),
	).N(func(s *Session, _ []string) error {
		s.cfg().AutosaveOn = false
		s.journal("no autosave")
		return nil
	}).When(on)
	return []*Node{startup, autosave}
}

// writeKids: write file NAME, write terminal, write description TEXT.
func writeKids() []*Node {
	file := K("file", "Write the running configuration to a named file",
		A(KWord, "FILENAME", "File name (letters, digits, '.', '_', '-'; up to 32 characters)").R(cmdWriteFile))
	terminal := K("terminal", "Write the running configuration to the terminal").R(func(s *Session, _ []string) error {
		s.Printf("%s", s.cfg().Render())
		return nil
	})
	desc := K("description", "Write to NV memory with a description shown in the history list",
		A(KRest, "TEXT", "Description, up to 120 characters").R(func(s *Session, v []string) error {
			d := strings.TrimSpace(v[0])
			if d == "" || len(d) > 120 {
				return fmt.Errorf("description must be 1-120 characters")
			}
			for _, r := range d {
				if r < 0x20 || r == 0x7f {
					return fmt.Errorf("description must not contain control characters")
				}
			}
			s.Printf("Building configuration...\n")
			if err := s.R.SaveDesc(d); err != nil {
				return err
			}
			s.journal("write description %q", d)
			s.Printf("[OK]\n")
			return nil
		})).When(featOn("config-features"))
	return []*Node{file, terminal, desc}
}

func cmdWriteFile(s *Session, v []string) error {
	ref, err := s.R.ref(v[0])
	if err != nil {
		return err
	}
	if ref.Kind != "file" {
		return fmt.Errorf("file name %q is reserved", v[0])
	}
	do := func(s *Session) error {
		if err := writeAtomic(ref.Path, s.cfg().Render()); err != nil {
			return err
		}
		s.journal("write file %s", ref.Name)
		s.Printf("[OK] running-config written to file %s\n", ref.Name)
		return nil
	}
	if _, err := os.Stat(ref.Path); err == nil {
		return s.ask(fmt.Sprintf("%% File %s already exists and will be overwritten.\n", ref.Name), "Continue?", do)
	}
	return do(s)
}

// copyKids: crash-config is a built-in recovery file; ordinary file-to-running and startup-to-running copies need feature config-features.
func copyKids() []*Node {
	manage := featOn("config-features")
	// the source is a saved file (write file NAME); the startup-config and crash-config sources are
	// the separate 'copy startup-config …' and 'copy crash-config …' commands
	file := K("file", "Copy a saved configuration file",
		A(KWord, "FILENAME", "Saved configuration file (see 'show files config')",
			K("startup-config", "Replace startup-config with this file").R(copyHandler("startup", "")),
			K("running-config", "Replace the running configuration with this file and apply it").R(copyHandler("running", "")).When(manage)).Cmp(savedFileNames))
	startup := K("startup-config", "Copy from the startup configuration",
		K("running-config", "Replace the running configuration with startup-config and apply it").R(copyHandler("running", "startup")).When(manage))
	crash := K("crash-config", "Copy from the configuration saved after an abnormal shutdown",
		K("startup-config", "Replace startup-config with crash-config").R(copyHandler("startup", "crash")),
		K("running-config", "Replace the running configuration with crash-config and apply it").R(copyHandler("running", "crash")))
	return []*Node{file, startup, crash}
}

// copyHandler: dst is "startup" or "running"; fixed is the source name if it is given by a command word.
func copyHandler(dst, fixed string) Handler {
	return func(s *Session, v []string) error {
		src := fixed
		if src == "" {
			src = v[0]
		}
		ref, err := s.R.ref(src)
		if err != nil {
			return err
		}
		if ref.Kind == dst {
			return fmt.Errorf("source and destination are the same")
		}
		text, err := s.R.readCfg(ref)
		if err != nil {
			return err
		}
		cfg, errs := s.R.validateConfig(text)
		if len(errs) > 0 {
			return fmt.Errorf("%s has errors, nothing was changed:\n%% %s", ref.Name, errList(errs, 5))
		}
		if dst == "startup" {
			warn := fmt.Sprintf("%% Warning: startup-config will be overwritten with %s.\n", ref.Name)
			if s.cfg().Features["config-features"] {
				warn += "%          The old startup-config is kept in the history.\n"
			} else {
				warn += "%          The old startup-config will be lost.\n"
			}
			return s.ask(warn, "Continue?", func(s *Session) error {
				if err := s.R.writeStartup(text); err != nil {
					return err
				}
				s.journal("copy %s startup-config", ref.Name)
				s.Printf("[OK] startup-config replaced. It takes effect when the daemon restarts; the running configuration is unchanged.\n")
				return nil
			})
		}
		warn := fmt.Sprintf("%% Warning: the running configuration will be replaced with %s.\n%%          Unsaved changes will be lost.\n", ref.Name)
		return s.ask(warn, "Continue?", func(s *Session) error {
			if err := s.loadRunning(cfg); err != nil {
				return err
			}
			s.journal("copy %s running-config", ref.Name)
			s.Printf("[OK] running-config replaced with %s\n", ref.Name)
			return nil
		})
	}
}

// deleteNode: delete config file NAME (feature config-features).
func deleteNode() *Node {
	manage := featOn("config-features")
	deleteCrash := func(name string) *Node {
		return K(name, "Delete the crash recovery configuration").R(func(s *Session, _ []string) error {
			return cmdDeleteConfig(s, []string{"crash"})
		})
	}
	return K("delete", "Delete a file",
		K("config", "Delete a saved configuration file",
			K("file", "Delete a configuration file or a history copy",
				deleteCrash("crash-config"),
				A(KWord, "FILENAME", "Saved configuration file (see 'show files config')").Cmp(savedFileNames).R(cmdDeleteConfig).When(manage))))
}

func cmdDeleteConfig(s *Session, v []string) error {
	ref, err := s.R.ref(v[0])
	if err != nil {
		return err
	}
	switch ref.Kind {
	case "file", "history", "crash":
	default:
		return fmt.Errorf("%s cannot be deleted", ref.Name)
	}
	if _, err := os.Stat(ref.Path); err != nil {
		return fmt.Errorf("file %q not found", ref.Name)
	}
	return s.ask(fmt.Sprintf("%% File %s will be deleted.\n", ref.Name), "Continue?", func(s *Session) error {
		if err := os.Remove(ref.Path); err != nil {
			return err
		}
		s.journal("delete config file %s", ref.Name)
		s.Printf("[OK] %s deleted\n", ref.Name)
		return nil
	})
}

// showConfigNodes: show files config, show config file/diff/history/journal.
func showConfigNodes() []*Node {
	files := K("files", "Show saved files",
		K("config", "List saved configuration files").R(func(s *Session, _ []string) error {
			es := s.R.listFiles()
			if len(es) == 0 {
				s.Printf("No saved configuration files (use 'write' or 'write file NAME').\n")
				return nil
			}
			s.Printf("%s", formatEntries(es))
			return nil
		}))
	file := K("file", "Display the contents of a saved configuration file",
		A(KWord, "FILENAME", "startup-config, crash-config, running-config or a saved file (see 'show files config')").Cmp(savedFileNames).R(func(s *Session, v []string) error {
			ref, err := s.R.ref(v[0])
			if err != nil {
				return err
			}
			text, err := s.R.readCfg(ref)
			if err != nil {
				return err
			}
			s.Printf("%s", text)
			return nil
		}))
	diff := K("diff", "Compare two configurations: lines only in the first (-) and only in the second (+)",
		A(KWord, "A", "First: startup-config, crash-config, running-config or a saved file",
			A(KWord, "B", "Second: startup-config, crash-config, running-config or a saved file").R(func(s *Session, v []string) error {
				var texts [2]string
				for i := 0; i < 2; i++ {
					ref, err := s.R.ref(v[i])
					if err != nil {
						return err
					}
					if texts[i], err = s.R.readCfg(ref); err != nil {
						return err
					}
				}
				d, err := diffText(texts[0], texts[1])
				if err != nil {
					return err
				}
				if d == "" {
					s.Printf("The configurations are identical.\n")
					return nil
				}
				s.Printf("%s", d)
				return nil
			}))).When(featOn("config-features"))
	history := K("history", "List copies kept by config-features: startup-config history and autosave").R(func(s *Session, _ []string) error {
		es := s.R.listHistoryEntries()
		if len(es) == 0 {
			s.Printf("The history is empty.\n")
			return nil
		}
		s.Printf("%s", formatEntries(es))
		return nil
	}).When(featOn("config-features"))
	journal := K("journal", "Show the last operations with configuration files (default 50)",
		A(KInt, "N", "Number of entries (1-1000)").Rng(1, 1000).R(func(s *Session, v []string) error {
			return showJournal(s, v[0])
		})).R(func(s *Session, _ []string) error { return showJournal(s, "50") }).When(featOn("config-features"))
	return []*Node{files, K("config", "Saved configuration files", file, diff, history, journal)}
}

func showJournal(s *Session, n string) error {
	var k int
	fmt.Sscan(n, &k)
	lines := s.R.journalTail(k)
	if len(lines) == 0 {
		s.Printf("The journal is empty.\n")
		return nil
	}
	s.Printf("%s\n", strings.Join(lines, "\n"))
	return nil
}
