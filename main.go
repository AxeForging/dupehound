package main

import (
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

	app := cli.NewApp()
	app.Name = "dupehound"
	app.Usage = "Detect code duplication across multiple languages"
	app.Version = Version

	app.Commands = []cli.Command{
		{
			Name:    "scan",
			Aliases: []string{"s"},
			Usage:   "Scan a directory or file for duplicate code",
			Flags:   []cli.Flag{pathFlag, minTokensFlag, minLinesFlag, formatFlag, outputFlag, verboseFlag, excludeFlag, languageFlag},
			Action:  scanAction.Execute,
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
		helpers.Log.Fatal().Err(err).Msg("fatal error")
	}
}
