package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/AxeForging/dupehound/actions"
	"github.com/AxeForging/dupehound/helpers"
	"github.com/AxeForging/dupehound/services"
	"github.com/urfave/cli"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

func main() {
	helpers.SetupLogger("info")

	scannerSvc := services.NewScannerService()
	scanAction := actions.NewScanAction(scannerSvc)
	initAction := actions.NewInitAction()

	app := cli.NewApp()
	app.Name = "dupehound"
	app.Usage = "Detect code duplication across multiple languages"
	app.Version = Version

	app.Commands = []cli.Command{
		{
			Name:    "scan",
			Aliases: []string{"s"},
			Usage:   "Scan a directory or file for duplicate code",
			Flags:   []cli.Flag{pathFlag, minTokensFlag, minLinesFlag, formatFlag, outputFlag, verboseFlag, excludeFlag, languageFlag, exitZeroFlag, similarityFlag, configFlag},
			Action:  scanAction.Execute,
		},
		{
			Name:  "init",
			Usage: "Create a starter .dupehound.yml config file in the current directory",
			Flags: []cli.Flag{
				cli.BoolFlag{Name: "force", Usage: "Overwrite existing .dupehound.yml"},
			},
			Action: initAction.Execute,
		},
		{
			Name:  "version",
			Usage: "Show version information",
			Action: func(c *cli.Context) error {
				fmt.Printf("dupehound version %s\n", Version)
				fmt.Printf("Build time: %s\n", BuildTime)
				fmt.Printf("Git commit: %s\n", GitCommit)
				return nil
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		if errors.Is(err, helpers.ErrClonesFound) {
			// Clones already printed — exit 1 to signal findings to the caller.
			os.Exit(1)
		}
		helpers.Log.Error().Err(err).Msg("error")
		os.Exit(2)
	}
}
