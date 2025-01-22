package repo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type GitlabRepo struct {
	ProjectId, Token, BranchName string
	Files                        []RepoTreeResponse
	FileNames                    []string
}
type LocalRepo struct {
	Directory, BranchName string
}
type FileAction struct {
	FilePath    string `json:"file_path"`
	Action      string `json:"action"`
	Content     string `json:"content"`
	AuthorEmail string `json:"author_email"`
	AuthorName  string `json:"author_name"`
}
type FilesCommit struct {
	Branch        string       `json:"branch"`
	CommitMessage string       `json:"commit_message"`
	Actions       []FileAction `json:"actions"`
}

func NewGitlabRepo() GitlabRepo {
	// Just a quick function to return a basic repo object
	if os.Getenv("ANANKE_REPO") == "" || os.Getenv("ANANKE_REPO_PAT") == "" {
		panic("Please set ANANKE_REPO and ANANKE_REPO_PAT environment variables")
	}
	repo := GitlabRepo{
		ProjectId: os.Getenv("ANANKE_REPO"),
		Token:     os.Getenv("ANANKE_REPO_PAT"),
	}
	// It's handy to have a list of all the files in the repo, so we do that here.
	repo.Files = repo.ListFiles()
	for _, file := range repo.Files {
		repo.FileNames = append(repo.FileNames, file.Path)
	}
	return repo
}

func (repo GitlabRepo) GetYamlContent(path string) map[string]interface{} {
	file := repo.GetFile(path)
	var data map[string]interface{}
	if file.StatusCode == 200 {
		body, _ := io.ReadAll(file.Body)
		err := yaml.Unmarshal(body, &data)
		if err != nil {
			fmt.Println(err)
		}
	}
	return data
}

func (repo GitlabRepo) GetHostPrefix(hostname string) string {
	// Get the file path prefix for a hostname in the repo
	for _, file := range repo.Files {
		pathParts := strings.Split(file.Path, "/")
		if len(pathParts) > 2 && pathParts[len(pathParts)-2] == hostname {
			return strings.Join(pathParts[:len(pathParts)-1], "/")
		}
	}
	panic("No prefix found for hostname: " + hostname)
}

func (repo GitlabRepo) GetFile(path string) *http.Response {
	path = strings.Replace(path, "/", "%2F", -1)
	if repo.BranchName == "" {
		repo.BranchName = "main"
	}
	urlBranchName := strings.Replace(repo.BranchName, "/", "%2F", -1)
	fileResponse := repo.Api("GET", "repository/files/"+path+"/raw?ref="+urlBranchName, []byte(""))
	return fileResponse
}

func (repo GitlabRepo) CommitFiles(commitMessage string, actions []FileAction) {
	if repo.BranchName == "" {
		panic("Cannot commit without branch")
	}
	filesCommit := FilesCommit{repo.BranchName, commitMessage, actions}
	body, err := json.Marshal(filesCommit)
	if err != nil {
		fmt.Println(err)
	}
	repo.Api("POST", "repository/commits", body)
}

type RepoTreeResponse struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Type string `json:"type"`
}

func (repo GitlabRepo) ListDevices() []string {
	devices := []string{}
	for _, file := range repo.Files {
		pathMembers := strings.Split(file.Path, "/")
		if len(pathMembers) == 4 && pathMembers[1] == "devices" {
			devices = append(devices, pathMembers[3])
		}
	}
	return devices
}

func (repo GitlabRepo) ListFiles() []RepoTreeResponse {
	files := repo.Api("GET", "repository/tree?recursive=true&per_page=10000", []byte(""))
	body, _ := io.ReadAll(files.Body)
	var data []RepoTreeResponse
	err := json.Unmarshal(body, &data)
	if err != nil {
		fmt.Println(err)
	}
	return data
}

func (repo GitlabRepo) Api(method string, suffix string, body []byte) *http.Response {
	// Generic API call function. Takes method, suffix, and body, and returns response.
	client := http.Client{}
	url := "https://gitlab.com/api/v4/projects/" + repo.ProjectId + "/" + suffix
	req, err := http.NewRequest(
		method,
		url,
		bytes.NewBuffer(body),
	)
	if err != nil {
		fmt.Println(err)
	}
	req.Header = http.Header{
		"PRIVATE-TOKEN": []string{repo.Token},
		"Content-Type":  []string{"application/json"},
	}
	resp, err := client.Do(req)
	statusOK := resp.StatusCode >= 200 && resp.StatusCode < 300
	if !statusOK {
		responseBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			fmt.Println(readErr)
		}
		// skip error for branch creation if branch already exists
		if string(responseBody) == "{\"message\": \"Branch already exists\"}" {
			return resp
		}
		fmt.Println(string(url))
		fmt.Println(string(responseBody))
	}
	if err != nil {
		fmt.Println(err)
	}
	return resp
}

func (repo GitlabRepo) CreateBranch() {
	urlBranchName := strings.Replace(repo.BranchName, "/", "%2F", -1)
	repo.Api("POST", "repository/branches?branch="+urlBranchName+"&ref=main", []byte(""))
}

type DiffResponse struct {
	Commits []struct {
		Id string `json:"id"`
	} `json:"commits"`
	Diffs []struct {
		Diff    string `json:"diff"`
		NewPath string `json:"new_path"`
		OldPath string `json:"old_path"`
	} `json:"diffs"`
}

func (repo GitlabRepo) DiffBranches(fromBranch, toBranch string) DiffResponse {
	diffResponse := DiffResponse{}
	diffs := repo.Api(
		"GET", "repository/compare?from="+fromBranch+"&to="+toBranch, []byte(""))
	body, err := io.ReadAll(diffs.Body)
	if err != nil {
		fmt.Println(err)
	}
	json.Unmarshal(body, &diffResponse)
	return diffResponse
}

func (repo GitlabRepo) DeleteBranch() {
	urlBranchName := strings.Replace(repo.BranchName, "/", "%2F", -1)
	repo.Api("DELETE", "repository/branches/"+urlBranchName, []byte(""))
}

func (repo GitlabRepo) CreatePr(prTitle string) string {
	// Create PR from branch. If there are no diffs, then delete branch and exit.
	if repo.BranchName == "" {
		return "A branch has not been created, cannot create PR"
	}
	diffResponse := repo.DiffBranches("main", repo.BranchName)
	if len(diffResponse.Diffs) == 0 {
		repo.DeleteBranch()
		return "No changes, deleting branch"
	}
	body := map[string]string{"title": prTitle, "source_branch": repo.BranchName, "target_branch": "main"}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		fmt.Println(err)
	}
	prResponse := map[string]interface{}{}
	response := repo.Api("POST", "merge_requests", []byte(bodyBytes))
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		fmt.Println(err)
	}
	json.Unmarshal(responseBody, &prResponse)
	return prResponse["web_url"].(string)
}

func (repo GitlabRepo) GetBranches() []string {
	// Get list of branches in repo
	branches := repo.Api("GET", "repository/branches", []byte(""))
	body, err := io.ReadAll(branches.Body)
	if err != nil {
		fmt.Println(err)
	}
	var data []map[string]interface{}
	err = json.Unmarshal(body, &data)
	if err != nil {
		fmt.Println(err)
	}
	branchNames := []string{}
	for _, branch := range data {
		branchNames = append(branchNames, branch["name"].(string))
	}
	return branchNames
}

func (repo GitlabRepo) CommitFilesAndCreatePr(actions []FileAction, branchName, commit_message, prTitle string) string {
	// Wrapper function to do everything important at once. Will create a branch if one
	// doesn't exist, commit files, and create PR (which in turn will check for diff
	// and delete branch if no diff).
	timeStamp := time.Now().Unix()
	if branchName == "" {
		branchName = "feature/niac-config-" + fmt.Sprint(timeStamp)
	}
	repo.BranchName = branchName
	repo.CreateBranch()
	repo.CommitFiles(commit_message, actions)
	prUrl := repo.CreatePr(prTitle)
	return prUrl
}
