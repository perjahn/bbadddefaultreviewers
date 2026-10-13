package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

func parseRepositoryUrl(rawUrl string) (projectKey, repoSlug, baseUrl string, err error) {
	parsed, err := url.Parse(rawUrl)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", "", fmt.Errorf("invalid repository url %q", rawUrl)
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for index := 0; index+2 < len(segments); index++ {
		if segments[index] == "projects" && segments[index+2] == "repos" && index+3 < len(segments) {
			projectKey = segments[index+1]
			repoSlug = segments[index+3]
			basePath := strings.Join(segments[:index], "/")
			baseUrl = parsed.Scheme + "://" + parsed.Host
			if basePath != "" {
				baseUrl += "/" + basePath
			}
			return projectKey, strings.TrimSuffix(repoSlug, ".git"), baseUrl, nil
		}
		if segments[index] == "scm" && index+2 < len(segments) {
			projectKey = segments[index+1]
			repoSlug = segments[index+2]
			basePath := strings.Join(segments[:index], "/")
			baseUrl = parsed.Scheme + "://" + parsed.Host
			if basePath != "" {
				baseUrl += "/" + basePath
			}
			return projectKey, strings.TrimSuffix(repoSlug, ".git"), baseUrl, nil
		}
	}
	return "", "", "", fmt.Errorf("repository url must contain /projects/{projectKey}/repos/{repoSlug} or /scm/{projectKey}/{repoSlug}")
}

func configureDefaultReviewers(baseUrl, projectKey, repoSlug, sourceBranch, targetBranch string, requiredApprovers int, users []string, creds bitbucketCredentials, dryRun bool, output io.Writer) error {
	resourceUrl := strings.TrimRight(baseUrl, "/") + "/rest/default-reviewers/1.0/projects/" + url.PathEscape(projectKey) + "/repos/" + url.PathEscape(repoSlug)
	client := &http.Client{Timeout: 30 * time.Second}

	conditions, err := getRepositoryConditions(client, resourceUrl+"/conditions", creds)
	if err != nil {
		return err
	}

	found := false
	var matchedCondition reviewerCondition
	for _, condition := range conditions {
		if isSameCondition(condition.SourceRefMatcher, sourceBranch, condition.TargetRefMatcher, targetBranch) {
			found = true
			matchedCondition = condition
			break
		}
	}

	conditionUrl := resourceUrl + "/condition"
	method := http.MethodPost
	existingBitbucketUsers := make([]bitbucketUser, 0)
	if found {
		conditionUrl += "/" + strconv.Itoa(matchedCondition.ID)
		method = http.MethodPut
		for _, reviewer := range matchedCondition.Reviewers {
			existingBitbucketUsers = append(existingBitbucketUsers, bitbucketUser{ID: reviewer.ID, Name: reviewer.Name, DisplayName: reviewer.DisplayName})
		}
	}

	payload := reviewerConditionRequest{
		SourceMatcher: branchMatcher{
			ID:        getMatcherId(sourceBranch),
			DisplayID: getMatcherDisplayId(sourceBranch),
			Type:      getMatcherType(sourceBranch),
			Active:    true,
		},
		TargetMatcher: branchMatcher{
			ID:        getMatcherId(targetBranch),
			DisplayID: getMatcherDisplayId(targetBranch),
			Type:      getMatcherType(targetBranch),
			Active:    true,
		},
		RequiredApprovals: requiredApprovers,
	}

	desiredBitbucketUsers := make([]bitbucketUser, 0)
	for _, username := range users {
		user, err := resolveBitbucketUser(client, baseUrl, username, creds)

		if errors.Is(err, ErrNoUserFound) {
			fmt.Printf("Ignoring user not found in bitbucket: '%s'\n", username)
			continue
		}

		if err != nil {
			return fmt.Errorf("failed to resolve user '%s': %w", username, err)
		}
		desiredBitbucketUsers = append(desiredBitbucketUsers, user)
	}

	changes := calculateReviewerDiff(existingBitbucketUsers, desiredBitbucketUsers)
	if err := printReviewerDiff(output, changes, found, matchedCondition, payload); err != nil {
		return fmt.Errorf("print reviewer diff: %w", err)
	}

	if len(changes.Added) == 0 && len(changes.Removed) == 0 {
		fmt.Printf("No changes.\n")
		return nil
	}

	if dryRun {
		return nil
	}
	return applyCondition(client, method, conditionUrl, payload, desiredBitbucketUsers, creds)
}

var ErrNoUserFound = errors.New("no user found")

func resolveBitbucketUser(client *http.Client, baseUrl, username string, creds bitbucketCredentials) (bitbucketUser, error) {
	queryUrl, err := url.Parse(baseUrl + "/rest/api/latest/admin/users")
	if err != nil {
		return bitbucketUser{}, fmt.Errorf("failed to parse admin users lookup url: %w", err)
	}
	q := queryUrl.Query()
	q.Set("filter", username)
	queryUrl.RawQuery = q.Encode()

	request, err := http.NewRequest(http.MethodGet, queryUrl.String(), nil)
	if err != nil {
		return bitbucketUser{}, fmt.Errorf("failed to create user lookup request: %w", err)
	}

	request.SetBasicAuth(creds.Username, creds.Token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return bitbucketUser{}, fmt.Errorf("failed to lookup bitbucket user '%s': %w", username, err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return bitbucketUser{}, fmt.Errorf("failed to read bitbucket user lookup response for '%s': %d: %w", username, response.StatusCode, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return bitbucketUser{}, fmt.Errorf("unexpected status %d while resolving '%s': '%s'", response.StatusCode, username, string(responseBody))
	}

	var users bitbucketUsers
	if err := json.Unmarshal(responseBody, &users); err != nil {
		return bitbucketUser{}, fmt.Errorf("failed to parse bitbucket user lookup response for '%s': %d: '%s' %w", username, response.StatusCode, string(responseBody), err)
	}
	if len(users.Values) == 0 {
		return bitbucketUser{}, ErrNoUserFound
	}
	if len(users.Values) > 1 {
		names := make([]string, 0, len(users.Values))
		for _, value := range users.Values {
			names = append(names, value.Name)
		}

		return bitbucketUser{}, fmt.Errorf("found multiple users for username '%s': %v", username, names)
	}
	if strings.TrimSpace(users.Values[0].Name) == "" {
		return bitbucketUser{}, fmt.Errorf("found empty username for '%s'", username)
	}

	return bitbucketUser{ID: users.Values[0].ID, Name: users.Values[0].Name, DisplayName: users.Values[0].DisplayName}, nil
}

func getMatcherId(branch string) string {
	if branch == "" {
		return "ANY_REF_MATCHER_ID"
	}
	if strings.HasPrefix(branch, "refs/heads/") {
		return branch
	}
	return "refs/heads/" + branch
}

func getMatcherDisplayId(branch string) string {
	if branch == "" {
		return "ANY_REF_MATCHER_ID"
	}
	return strings.TrimPrefix(branch, "refs/heads/")
}

func getMatcherType(branch string) matcherType {
	if branch == "" {
		return matcherType{ID: "ANY_REF", Name: "Any branch"}
	}
	return matcherType{ID: "BRANCH", Name: "Branch"}
}

func isSameCondition(sourceMatcher branchMatcher, sourceBranch string, targetMatcher branchMatcher, targetBranch string) bool {
	if ((sourceMatcher.Type.ID == "BRANCH" && (sourceMatcher.ID == sourceBranch || sourceMatcher.DisplayID == sourceBranch)) || (sourceMatcher.Type.ID == "ANY_REF" && sourceBranch == "")) &&
		((targetMatcher.Type.ID == "BRANCH" && (targetMatcher.ID == targetBranch || targetMatcher.DisplayID == targetBranch)) || (targetMatcher.Type.ID == "ANY_REF" && targetBranch == "")) {
		return true
	}
	return false
}

func calculateReviewerDiff(existing []bitbucketUser, desired []bitbucketUser) reviewerChanges {
	existingByID := make(map[int]bitbucketUser, len(existing))
	desiredByID := make(map[int]bitbucketUser, len(desired))
	for _, user := range existing {
		existingByID[user.ID] = user
	}
	for _, user := range desired {
		desiredByID[user.ID] = user
	}
	changes := reviewerChanges{}
	for key, user := range desiredByID {
		if _, found := existingByID[key]; !found {
			changes.Added = append(changes.Added, user)
		}
	}
	for key, user := range existingByID {
		if _, found := desiredByID[key]; !found {
			changes.Removed = append(changes.Removed, user)
		}
	}
	sort.Slice(changes.Added, func(i, j int) bool {
		return strings.ToLower(changes.Added[i].Name) < strings.ToLower(changes.Added[j].Name)
	})
	sort.Slice(changes.Removed, func(i, j int) bool {
		return strings.ToLower(changes.Removed[i].Name) < strings.ToLower(changes.Removed[j].Name)
	})
	return changes
}

func printReviewerDiff(output io.Writer, changes reviewerChanges, found bool, existing reviewerCondition, payload reviewerConditionRequest) error {
	if _, err := fmt.Fprintf(output, "Reviewers to add (%d):\n", len(changes.Added)); err != nil {
		return err
	}

	if err := printUserDiff(output, changes.Added, "+"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "Reviewers to remove (%d):\n", len(changes.Removed)); err != nil {
		return err
	}
	if err := printUserDiff(output, changes.Removed, "-"); err != nil {
		return err
	}

	if found {
		_, err := fmt.Fprintf(output, "Found existing branch condition:\n")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(output, "  %s (%s) (%s) -> %s (%s) (%s)\n",
			existing.SourceRefMatcher.ID, existing.SourceRefMatcher.DisplayID, existing.SourceRefMatcher.Type.ID,
			existing.TargetRefMatcher.ID, existing.TargetRefMatcher.DisplayID, existing.TargetRefMatcher.Type.ID)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(output, "  %s (%s) (%s) -> %s (%s) (%s)\n",
			payload.SourceMatcher.ID, payload.SourceMatcher.DisplayID, payload.SourceMatcher.Type.ID,
			payload.TargetMatcher.ID, payload.TargetMatcher.DisplayID, payload.TargetMatcher.Type.ID)
		if err != nil {
			return err
		}
	} else {
		_, err := fmt.Fprintf(output, "Didn't find existing branch condition:\n")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(output, "  %s (%s) (%s) -> %s (%s) (%s)\n",
			payload.SourceMatcher.ID, payload.SourceMatcher.DisplayID, payload.SourceMatcher.Type.ID,
			payload.TargetMatcher.ID, payload.TargetMatcher.DisplayID, payload.TargetMatcher.Type.ID)
		if err != nil {
			return err
		}
	}

	if found {
		_, err := fmt.Fprintf(output, "Required approvals: %d -> %d\n", existing.RequiredApprovals, payload.RequiredApprovals)
		if err != nil {
			return err
		}
	} else {
		_, err := fmt.Fprintf(output, "Required approvals: (new condition) -> %d\n", payload.RequiredApprovals)
		if err != nil {
			return err
		}
	}

	return nil
}

func printUserDiff(output io.Writer, users []bitbucketUser, prefix string) error {
	if len(users) == 0 {
		_, err := fmt.Fprintln(output, "  (none)")
		return err
	}
	for _, user := range users {
		if _, err := fmt.Fprintf(output, "  %s %s (%s)\n", prefix, user.Name, user.DisplayName); err != nil {
			return err
		}
	}
	return nil
}

func getRepositoryConditions(client *http.Client, endpoint string, creds bitbucketCredentials) ([]reviewerCondition, error) {
	conditions := make([]reviewerCondition, 0)
	start := 0
	const pageSize = 100
	for {
		requestUrl, err := url.Parse(endpoint)
		if err != nil {
			return nil, fmt.Errorf("build bitbucket conditions url: %w", err)
		}
		query := requestUrl.Query()
		query.Set("start", strconv.Itoa(start))
		query.Set("limit", strconv.Itoa(pageSize))
		requestUrl.RawQuery = query.Encode()
		request, err := http.NewRequest(http.MethodGet, requestUrl.String(), nil)
		if err != nil {
			return nil, err
		}
		request.SetBasicAuth(creds.Username, creds.Token)
		request.Header.Set("Accept", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("get bitbucket default-reviewer conditions: %w", err)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read bitbucket conditions response: %w", readErr)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("bitbucket returned http %d listing conditions: %s", response.StatusCode, strings.TrimSpace(string(body)))
		}
		var page []reviewerCondition
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("decode bitbucket conditions response: %w; body: '%s'", err, string(body))
		}
		conditions = append(conditions, page...)
		if len(page) < pageSize {
			return conditions, nil
		}
		start += len(page)
	}
}

func applyCondition(client *http.Client, method, endpoint string, payload reviewerConditionRequest, bitbucketUsers []bitbucketUser, creds bitbucketCredentials) error {
	reviewerIDs := make([]bitbucketUserID, len(bitbucketUsers))
	for i := range bitbucketUsers {
		reviewerIDs[i].ID = bitbucketUsers[i].ID
	}
	payload.Reviewers = reviewerIDs
	status, responseBody, err := sendCondition(client, method, endpoint, payload, creds)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("bitbucket returned http %d: %s", status, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

func sendCondition(client *http.Client, method, endpoint string, payload reviewerConditionRequest, creds bitbucketCredentials) (int, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}

	request, err := http.NewRequest(method, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.SetBasicAuth(creds.Username, creds.Token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")

	if os.Getenv("BITBUCKET_DEBUG_PAYLOADS") == "true" {
		if err := os.WriteFile("request_payload.json", body, 0600); err != nil {
			return 0, nil, fmt.Errorf("write debug file: %w", err)
		}
	}

	response, err := client.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("call bitbucket default-reviewers api: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return response.StatusCode, nil, fmt.Errorf("read bitbucket response: %w", err)
	}

	if os.Getenv("BITBUCKET_DEBUG_PAYLOADS") == "true" {
		if err := os.WriteFile("response_payload.json", responseBody, 0600); err != nil {
			return 0, nil, fmt.Errorf("write debug file: %w", err)
		}
	}

	return response.StatusCode, responseBody, nil
}
