// timescaledb-tune analyzes a user's postgresql.conf file to make sure it is
// ready and tuned to use TimescaleDB. It checks that the TimescaleDB library
// ('timescaledb') is properly listed as a shared preload library and analyzes
// various groups of settings to make sure they are reasonably set for the
// machine's resources.
//
// The groups of settings deal with memory usage, parallelism, the WAL, and
// other miscellaneous settings that have been found to be useful when tuning.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"strings"

	"github.com/caarlos0/env/v11"
	"github.com/timescale/timescaledb-tune/pkg/pgtune"
	"github.com/timescale/timescaledb-tune/pkg/tstune"
)

const (
	binName = "timescaledb-tune"
	version = tstune.Version
)

// parsedArgs holds the result of parsing the command line and environment.
type parsedArgs struct {
	flags       tstune.TunerFlags
	showVersion bool
}

// parseArgs builds TunerFlags from args and environ. Command-line flags take
// precedence over TSTUNE_* environment variables, which take precedence over
// built-in defaults. Errors are written to output before being returned.
func parseArgs(args []string, environ map[string]string, output io.Writer) (*parsedArgs, error) {
	// Seed built-in defaults, then overlay any TSTUNE_* env vars on top.
	defaults := tstune.TunerFlags{
		PGConfig:     "pg_config",
		MaxBGWorkers: pgtune.MaxBackgroundWorkersDefault,
		UseColor:     true,
	}
	if err := env.ParseWithOptions(&defaults, env.Options{Environment: environ}); err != nil {
		err = describeEnvError(err, environ)
		fmt.Fprintf(output, "%s: %v\n", binName, err)
		return nil, err
	}

	p := &parsedArgs{}
	f := &p.flags

	fs := flag.NewFlagSet(binName, flag.ContinueOnError)
	fs.SetOutput(output)
	fs.StringVar(&f.Memory, "memory", defaults.Memory, "Amount of memory to base recommendations on in the PostgreSQL format <int value><units>, e.g., 4GB. Default is to use all memory")
	fs.UintVar(&f.NumCPUs, "cpus", defaults.NumCPUs, "Number of CPU cores to base recommendations on. Default is equal to number of cores")
	fs.StringVar(&f.PGVersion, "pg-version", defaults.PGVersion, "Major version of PostgreSQL to base recommendations on. Default is determined via pg_config. Valid values: "+strings.Join(tstune.ValidPGVersions, ", "))
	fs.StringVar(&f.WALDiskSize, "wal-disk-size", defaults.WALDiskSize, "Size of the disk where the WAL resides, in PostgreSQL format <int value><units>, e.g., 4GB. Using this flag helps tune WAL behavior.")
	fs.Uint64Var(&f.MaxConns, "max-conns", defaults.MaxConns, "Max number of connections for the database. Default is equal to our best recommendation")
	fs.IntVar(&f.MaxBGWorkers, "max-bg-workers", defaults.MaxBGWorkers, "Max number of background workers")
	fs.StringVar(&f.ConfPath, "conf-path", defaults.ConfPath, "Path to postgresql.conf. If blank, heuristics will be used to find it")
	fs.StringVar(&f.DestPath, "out-path", defaults.DestPath, "Path to write the new configuration file. If blank, will use the same file that is read from")
	fs.StringVar(&f.PGConfig, "pg-config", defaults.PGConfig, "Path to the pg_config binary")
	fs.BoolVar(&f.YesAlways, "yes", defaults.YesAlways, "Answer 'yes' to every prompt")
	fs.BoolVar(&f.Quiet, "quiet", defaults.Quiet, "Show only the total recommendations at the end")
	fs.BoolVar(&f.UseColor, "color", defaults.UseColor, "Use color in output (works best on dark terminals)")
	fs.BoolVar(&f.DryRun, "dry-run", defaults.DryRun, "Whether to just show the changes without overwriting the configuration file")
	fs.BoolVar(&f.Restore, "restore", defaults.Restore, "Whether to restore a previously made conf file backup")
	fs.StringVar(&f.Profile, "profile", defaults.Profile, "a specific \"mode\" for tailoring recommendations to a special workload type. If blank or unspecified, a default is used. Valid values: \"promscale\"")

	fs.BoolVar(&p.showVersion, "version", false, "Show the version of this tool")

	fs.Usage = func() {
		fmt.Fprintf(output, "Usage of %s:\n", binName)
		fs.PrintDefaults()
		fmt.Fprintf(output, "\nEvery flag may also be set via a TSTUNE_<FLAG> environment variable\n(e.g. --max-bg-workers -> TSTUNE_MAX_BG_WORKERS). Flags take precedence.\n")
	}

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	return p, nil
}

// describeEnvError rewrites env parse errors so they name the offending
// environment variable (e.g. TSTUNE_CPUS) rather than the Go struct field.
func describeEnvError(err error, environ map[string]string) error {
	var agg env.AggregateError
	if !errors.As(err, &agg) {
		return err
	}
	t := reflect.TypeOf(tstune.TunerFlags{})
	var msgs []string
	for _, e := range agg.Errors {
		var pe env.ParseError
		if !errors.As(e, &pe) {
			msgs = append(msgs, e.Error())
			continue
		}
		key := pe.Name
		if sf, ok := t.FieldByName(pe.Name); ok {
			if tag := sf.Tag.Get("env"); tag != "" {
				key = strings.Split(tag, ",")[0]
			}
		}
		msgs = append(msgs, fmt.Sprintf("invalid value %q for environment variable %s: %v", environ[key], key, pe.Err))
	}
	return errors.New(strings.Join(msgs, "; "))
}

func main() {
	p, err := parseArgs(os.Args[1:], env.ToMap(os.Environ()), os.Stderr)
	if err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		os.Exit(2)
	}

	if p.showVersion {
		fmt.Printf("%s %s (%s %s)\n", binName, version, runtime.GOOS, runtime.GOARCH)
	} else {
		tuner := tstune.Tuner{}
		tuner.Run(&p.flags, os.Stdin, os.Stdout, os.Stderr)
	}
}
