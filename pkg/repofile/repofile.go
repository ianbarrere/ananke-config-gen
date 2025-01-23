package repofile

import (
	"slices"
	"sort"
	"strings"

	"github.com/ibarrere/ananke-config-gen/pkg/repo"
	"github.com/ibarrere/ananke-config-gen/pkg/repoconfig"
)

func GetFileActions(repoFiles map[string][]RepoFile, allRepoFiles []string, exportFormat repoconfig.ExportFormat) []repo.FileAction {
	// Get file actions for map of hostname to list of RepoFile objects (common return
	// format for many functions in this package)
	fileActions := []repo.FileAction{}
	for _, repoFilesList := range repoFiles {
		for _, repoFile := range repoFilesList {
			fileActions = append(fileActions, repoFile.GetFileAction(allRepoFiles, exportFormat))
		}
	}
	return fileActions
}

func (rf RepoFile) GetFileAction(allRepoFiles []string, exportFormat repoconfig.ExportFormat) repo.FileAction {
	// Get file action for RepoFile object
	var action string
	if slices.Contains(allRepoFiles, rf.FilePath) {
		action = "update"
	} else {
		action = "create"
	}
	return repo.FileAction{
		FilePath:    rf.FilePath,
		Action:      action,
		Content:     rf.GetContent(exportFormat),
		AuthorEmail: "ian.barrere@gmail.com",
		AuthorName:  "Ian Barrere",
	}
}

func (rf *RepoFile) GetContent(outputFormat repoconfig.ExportFormat) string {
	// Get YAML content of all config sections in RepoFile
	fileContent := []string{}
	for _, configSection := range rf.ConfigSections {
		fileContent = append(fileContent, configSection.Serialize(outputFormat))
	}
	return strings.Join(fileContent, "\n")
}

func InsertRepoConfig(rcs []repoconfig.RepoConfig, rc repoconfig.RepoConfig) []repoconfig.RepoConfig {
	// Insert RepoConfig into list, alphabetical by Path
	i := sort.Search(len(rcs), func(i int) bool { return rcs[i].Path > rc.Path })
	rcs = append(rcs, repoconfig.RepoConfig{})
	copy(rcs[i+1:], rcs[i:])
	rcs[i] = rc
	return rcs
}

func NewRepoFile(filePath string, configSections []repoconfig.RepoConfig) RepoFile {
	orderedConfigSections := []repoconfig.RepoConfig{}
	for _, configSection := range configSections {
		orderedConfigSections = InsertRepoConfig(orderedConfigSections, configSection)
	}
	return RepoFile{
		FilePath:       filePath,
		ConfigSections: orderedConfigSections,
	}
}

type YangObject interface {
	IsYANGGoStruct()
}

type RepoFile struct {
	FilePath       string
	ConfigSections []repoconfig.RepoConfig
}
