package notice

import (
	"go/ast"
	"go/token"
	"slices"
	"strings"
)

type commentAnchor struct {
	before string
	after  string
}

func commentAnchors(group *ast.CommentGroup, positions []syntaxPosition) commentAnchor {
	var before, after token.Pos
	var preceding, following []string

	for _, point := range positions {
		if point.position < group.Pos() {
			if point.position > before {
				before = point.position
				preceding = []string{point.path}

				continue
			}

			if point.position == before {
				preceding = append(preceding, point.path)
			}

			continue
		}

		if point.position < group.End() {
			continue
		}

		if !after.IsValid() || point.position < after {
			after = point.position
			following = []string{point.path}

			continue
		}

		if point.position == after {
			following = append(following, point.path)
		}
	}

	slices.Sort(preceding)
	slices.Sort(following)

	return commentAnchor{
		before: strings.Join(preceding, "\x00"),
		after:  strings.Join(following, "\x00"),
	}
}
