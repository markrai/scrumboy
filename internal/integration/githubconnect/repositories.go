package githubconnect

import (
    "context"
    "net/http"
    "strconv"
)

func (s *Service) userRepositories(ctx context.Context, token string, installationID int64) ([]int64, error) {
    seen := make(map[int64]bool)
    result := make([]int64, 0)
    total := -1
    for page := 1; page <= maxInstallationPages; page++ {
        var response struct {
            TotalCount int `json:"total_count"`
            Repositories []struct {
                ID int64 `json:"id"`
            } `json:"repositories"`
        }
        path := "/user/installations/" + strconv.FormatInt(installationID, 10) +
            "/repositories?per_page=100&page=" + strconv.Itoa(page)
        if err := s.request(ctx, http.MethodGet, path, token, "", s.apiBase, &response); err != nil {
            return nil, err
        }
        if response.TotalCount < 0 || response.TotalCount > maxInstallationPages*100 ||
            len(response.Repositories) > 100 || (total != -1 && total != response.TotalCount) {
            return nil, ErrUnavailable
        }
        total = response.TotalCount
        for _, repository := range response.Repositories {
            if repository.ID <= 0 || seen[repository.ID] { return nil, ErrUnavailable }
            seen[repository.ID] = true
            result = append(result, repository.ID)
        }
        if len(result) == total { return result, nil }
        if len(result) > total || len(response.Repositories) != 100 { return nil, ErrUnavailable }
    }
    return nil, ErrUnavailable
}
