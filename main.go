package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gdamore/tcell/v2"
)

var version = "0.4.2"

func run(paths []string) error {
	e := newEditor(nil)
	for _, path := range paths {
		if err := e.open(path); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	if len(e.buffers) == 0 {
		e.buffers = append(e.buffers, newBuffer())
	}
	e.switchTo(0)
	screen, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	if err = screen.Init(); err != nil {
		return err
	}
	defer screen.Fini()
	defer e.shutdownProject()
	e.screen = screen
	screen.EnablePaste()
	// Signals wake PollEvent so deferred terminal restoration runs on exit.
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-signals:
			screen.PostEvent(tcell.NewEventInterrupt(nil))
		case <-stop:
		}
	}()
	e.message = "Ctrl-G: help | F5/F6: switch buffers"
	for !e.done {
		e.draw()
		ev := screen.PollEvent()
		if ev == nil {
			break
		}
		if interrupt, ok := ev.(*tcell.EventInterrupt); ok && interrupt.Data() == nil {
			return fmt.Errorf("interrupted; unsaved edits were not written")
		}
		e.handle(ev)
	}
	return nil
}
func main() {
	showVersion := flag.Bool("version", false, "print version")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: atto [options] [file ...]\n\nA small multi-buffer terminal editor.\nCtrl-G: help; Ctrl-S: save; Ctrl-X: close; Ctrl-Q: quit.\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println("atto " + version)
		return
	}
	if err := run(flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "atto:", err)
		os.Exit(1)
	}
}
