package httpapi

import boardapp "scrumboy/internal/application/board"

const maxBoardTagFilters = boardapp.MaxTagFilters

func parseBoardTagFilters(values []string) ([]string, error) {
	return boardapp.NormalizeTagFilters(values)
}
