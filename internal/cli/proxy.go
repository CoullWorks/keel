package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/coullworks/keel/internal/proxy"
	"github.com/coullworks/keel/internal/tui"
)

// DefaultProxyPort is 80 so a bare hostname works with nothing after it. It is
// privileged on Linux and macOS, so the failure to bind explains the options
// rather than surfacing a raw permission error.
const DefaultProxyPort = 80

func proxyCmd() *cobra.Command {
	c := requireSubcommand(&cobra.Command{
		Use:   "proxy",
		Short: "Serve every running project at <name>.localhost, so ports stop mattering",
		Long: "A fixed port is a shared resource: two projects both wanting 3000 collide, and so\n" +
			"does anything else already holding it. keel proxy routes by project name instead,\n" +
			"and `keel run dev` takes whatever port the kernel offers.\n\n" +
			".localhost is reserved for loopback by RFC 6761, so the names resolve with no\n" +
			"hosts file to edit and no DNS to run.\n\n" +
			"DDEV projects already have a router and are reached at their .ddev.site name;\n" +
			"this is for the local and compose environments that do not.",
	})
	c.AddCommand(proxyStartCmd(), proxyStatusCmd())
	return c
}

func proxyStartCmd() *cobra.Command {
	var port int
	c := &cobra.Command{
		Use:   "start",
		Short: "Run the proxy in the foreground",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()

			table, err := proxy.LoadTable()
			if err != nil {
				return err
			}
			srv, err := proxy.Listen(port, table)
			if err != nil {
				if errors.Is(err, os.ErrPermission) || isPermission(err) {
					return fmt.Errorf("%w\n\nPort %d needs privileges. Either:\n"+
						"  sudo setcap 'cap_net_bind_service=+ep' $(command -v keel)\n"+
						"  keel proxy start --port 8080   (then use name.localhost:8080)",
						err, port)
				}
				return err
			}
			defer srv.Close()

			entries, _ := proxy.Load()
			fmt.Fprintf(out, "keel proxy on 127.0.0.1:%d\n", port)
			if len(entries) == 0 {
				fmt.Fprintln(out, "\nNothing is running yet. Start a project with: keel run dev")
			}
			for _, e := range entries {
				fmt.Fprintf(out, "  %s -> 127.0.0.1:%d\n", e.URL(port), e.Port)
			}

			// The table is rebuilt on every signal so a project that starts
			// after the proxy did becomes reachable without a restart.
			reload := make(chan os.Signal, 1)
			signal.Notify(reload, syscall.SIGHUP)
			stop := make(chan os.Signal, 1)
			signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

			errCh := make(chan error, 1)
			go func() { errCh <- srv.Serve() }()

			for {
				select {
				case <-reload:
					if t, err := proxy.LoadTable(); err == nil {
						for _, r := range t.Routes() {
							srv.Table.Set(r.Name, r.Port)
						}
						fmt.Fprintf(out, "reloaded: %d project(s)\n", len(t.Routes()))
					}
				case <-stop:
					fmt.Fprintln(out, "\nstopping")
					return nil
				case err := <-errCh:
					return err
				}
			}
		},
	}
	c.Flags().IntVar(&port, "port", DefaultProxyPort, "port to serve on")
	return c
}

func proxyStatusCmd() *cobra.Command {
	var port int
	var asJSON bool
	c := &cobra.Command{
		Use:   "status",
		Short: "List the projects the proxy can reach",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			entries, err := proxy.Load()
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(out).Encode(entries)
			}
			if len(entries) == 0 {
				fmt.Fprintln(out, "no projects are running. Start one with: keel run dev")
				return nil
			}
			rows := make([]tui.ProxyRow, len(entries))
			for i, e := range entries {
				rows[i] = tui.ProxyRow{Name: e.Name, URL: e.URL(port), PID: e.PID}
			}
			fmt.Fprintln(out, tui.RenderProxyStatus(rows))
			return nil
		},
	}
	c.Flags().IntVar(&port, "port", DefaultProxyPort, "the port the proxy is serving on, for the URLs printed")
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable output, for editors and scripts")
	return c
}

func isPermission(err error) bool {
	var se syscall.Errno
	return errors.As(err, &se) && (se == syscall.EACCES || se == syscall.EPERM)
}
