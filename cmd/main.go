package main

import (
	"fmt"
	"log"
	"os"
	"slices"

	"github.com/urfave/cli/v2"

	"github.com/ibarrere/ananke-config-gen/pkg/repo"
	"github.com/ibarrere/ananke-config-gen/pkg/repoconfig"
	"github.com/ibarrere/ananke-config-gen/pkg/repofile"
	"github.com/ibarrere/ananke-config-gen/pkg/tools/netbox"
)

func ConfigTypeMatches(configType string, configTypes []string) bool {
	// Check if config type is in list or if configTypes is empty
	if slices.Contains(configTypes, configType) || len(configTypes) == 0 {
		return true
	}
	return false
}

func main() {
	app := &cli.App{
		Name: "Ananke Config Generator",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "branch",
				Aliases: []string{"b"},
				Usage:   "Branch to commit to",
			},
			&cli.StringSliceFlag{
				Name:    "filter",
				Aliases: []string{"f"},
				Usage:   "Specific interface(s) or interface tags to configure",
			},
			&cli.StringSliceFlag{
				Name:    "config-type",
				Aliases: []string{"c"},
				Usage:   "Config type to generate: INTERFACES, OSPF, VLANS, LACP, ACL",
			},
			&cli.BoolFlag{
				Name:    "explicit-descriptions",
				Aliases: []string{"D"},
				Usage:   "Suppress automatic interface descriptions",
			},
			&cli.StringFlag{
				Name:    "output-format",
				Aliases: []string{"O"},
				Usage:   "Output format: JSON, YAML",
				Value:   "YAML",
			},
			&cli.BoolFlag{
				Name:    "stdout",
				Aliases: []string{"S"},
				Usage:   "Print outputs to CLI rather than commit to GitLab",
			},
			&cli.StringFlag{
				Name:  "interface-layout",
				Usage: "Interface layout: SEPARATE, SAMEFILE, TOGETHER",
				Value: "SEPARATE",
			},
		},
		Action: func(ctx *cli.Context) error {
			if ctx.NArg() == 0 {
				fmt.Println("Please provide at least one device name")
				return nil
			}
			if ctx.String("output-format") != "JSON" && ctx.String("output-format") != "YAML" {
				panic("Invalid output format: " + ctx.String("output-format") + " Valid output formats: JSON, YAML")
			}
			if ctx.String("interface-layout") != "SEPARATE" && ctx.String("interface-layout") != "SAMEFILE" && ctx.String("interface-layout") != "TOGETHER" {
				panic("Invalid interface layout: " + ctx.String("interface-layout") + " Valid interface layouts: SEPARATE, SAMEFILE, TOGETHER")
			}
			configTypes := []string{"INTERFACES", "OSPF", "VLANS", "ACL", "LACP"}
			for _, configType := range ctx.StringSlice("config-type") {
				if !slices.Contains(configTypes, configType) {
					fmt.Println(
						"Invalid config type: "+
							configType, "Valid config types: ", configTypes)
					return nil
				}
			}
			if !slices.Contains(ctx.StringSlice("config-type"), "INTERFACES") && len(ctx.StringSlice("filter")) > 0 {
				fmt.Println("--filter/-f flag only valid with INTERFACES config type")
				return nil
			}
			gitLabRepo := repo.NewGitlabRepo()
			cfn := netbox.NewConfigFromNetbox(ctx.Args().Slice())
			cfn.GetInterfaceDependentBindings(ctx.StringSlice("config-type"))
			if ConfigTypeMatches("INTERFACES", ctx.StringSlice("config-type")) {
				cfn.GetInterfaceBindings(ctx.StringSlice("filter"), !ctx.Bool("explicit-descriptions"))
			}
			if ConfigTypeMatches("VLANS", ctx.StringSlice("config-type")) {
				cfn.GetVlanBindings()
			}
			repoConfigFiles := cfn.GetRepoConfig(gitLabRepo, ctx.String("interface-layout"))
			if !ctx.Bool("stdout") {
				fileActions := repofile.GetFileActions(repoConfigFiles, gitLabRepo.FileNames, repoconfig.ExportFormat(ctx.String("output-format")))
				prUrl := gitLabRepo.CommitFilesAndCreatePr(
					fileActions,
					ctx.String("branch"),
					"[AUTO] ananke-config-gen from Netbox",
					"ananke-config-gen from Netbox")
				fmt.Println(prUrl)
			} else {
				for _, repoFilesList := range repoConfigFiles {
					for _, repoFile := range repoFilesList {
						fmt.Println(repoFile.GetContent(repoconfig.ExportFormat(ctx.String("output-format"))))
					}
				}
			}
			return nil
		},
	}
	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
