package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"
	"go.universe.tf/virtuakube"
)

type universeFlags struct {
	dir          string
	snapshot     string
	verbose      bool
	vmgraphics   bool
	acceleration bool
	wait         bool
	save         bool
	saveName     string
}

func addUniverseFlags(cmd *cobra.Command, flags *universeFlags, wait, save bool) {
	cmd.Flags().StringVarP(&flags.dir, "universe", "u", "", "directory containing the universe")
	cmd.Flags().StringVarP(&flags.snapshot, "snapshot", "s", "", "snapshot to resume in the universe")
	cmd.Flags().BoolVarP(&flags.verbose, "verbose", "v", false, "show commands being executed under the hood")
	cmd.Flags().BoolVar(&flags.vmgraphics, "graphics", false, "show a GUI for each running VM")
	cmd.Flags().BoolVar(&flags.acceleration, "acceleration", true, "use KVM to accelerate VMs")
	cmd.Flags().BoolVarP(&flags.wait, "wait", "w", wait, "wait for ctrl+C before exiting")
	cmd.Flags().BoolVar(&flags.save, "save", save, "save the universe on exit")
	cmd.Flags().StringVar(&flags.saveName, "save-snapshot", "", "snapshot to save to, if different from --snapshot")
	cmd.MarkFlagRequired("universe")
}

type universeFunc func(*virtuakube.Universe) error

func withUniverse(flags *universeFlags, do universeFunc) func(*cobra.Command, []string) {
	return func(_ *cobra.Command, _ []string) {
		if err := runDoWithUniverse(flags, do); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}
}

func runDoWithUniverse(flags *universeFlags, do universeFunc) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle ctrl+C by cancelling the context, which will shut down
	// everything in the universe.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	go func() {
		defer cancel()
		select {
		case <-stop:
		case <-ctx.Done():
		}
	}()

	start := time.Now()

	u, err := openOrCreateUniverse(flags.dir, flags.snapshot, flags.verbose, flags.vmgraphics, flags.wait, flags.acceleration)
	if err != nil {
		return fmt.Errorf("Getting universe: %v", err)
	}
	defer u.Close()

	if err := do(u); err != nil {
		return err
	}

	d := time.Since(start)
	switch {
	case d < time.Second:
		d = d.Truncate(time.Millisecond)
	case d < time.Second:
		d = d.Truncate(time.Second / 10)
	default:
		d = d.Truncate(time.Second)
	}
	fmt.Printf("Operation took %s.\n", d)

	if flags.wait {
		fmt.Printf("Resources available:\n\n")
		for _, cluster := range u.Clusters() {
			fmt.Printf("  Cluster %q: export KUBECONFIG=%q\n", cluster.Name(), cluster.Kubeconfig())
		}
		for _, vm := range u.VMs() {
			fmt.Printf("  VM %q: ssh -p%d root@localhost\n", vm.Hostname(), vm.ForwardedPort(22))
		}

		fmt.Println("\nHit ctrl+C to shut down")
		<-ctx.Done()
	}

	if flags.save {
		fmt.Println("Saving universe...")
		saveName := flags.saveName
		if saveName == "" && saveName != flags.snapshot {
			saveName = flags.snapshot
		}
		if err := u.Save(saveName); err != nil {
			return fmt.Errorf("Saving universe: %v", err)
		}
	} else {
		fmt.Println("Closing (and reverting) universe...")
		if err := u.Close(); err != nil {
			return fmt.Errorf("Closing universe: %v", err)
		}
	}

	return nil
}

// openOrCreateUniverse sets up a universe, either by creating it from
// scratch, or by opening an existing one.
func openOrCreateUniverse(dir, snapshot string, verbose, vmgraphics, interactive, acceleration bool) (*virtuakube.Universe, error) {
	if dir == "" {
		return nil, errors.New("universe directory not specified")
	}

	var (
		universe *virtuakube.Universe
		err      error
	)

	cfg := &virtuakube.UniverseConfig{
		VMGraphics:     vmgraphics,
		Interactive:    interactive,
		NoAcceleration: !acceleration,
	}
	if verbose {
		cfg.CommandLog = os.Stdout
	}

	_, err = os.Stat(dir)
	if os.IsNotExist(err) {
		universe, err = virtuakube.Create(dir, cfg)
	} else if err != nil {
		return nil, err
	} else {
		universe, err = virtuakube.Open(dir, snapshot, cfg)
	}
	if err != nil {
		return nil, fmt.Errorf("getting universe: %v", err)
	}

	return universe, nil
}
