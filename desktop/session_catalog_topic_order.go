package main

func projectTopicLess(left, right ProjectNode, sortMode string, manualOrder bool) bool {
	if left.Pinned != right.Pinned {
		return left.Pinned
	}
	if manualOrder {
		leftRank, rightRank := left.SortOrder, right.SortOrder
		if leftRank < 0 {
			leftRank = int(^uint(0) >> 1)
		}
		if rightRank < 0 {
			rightRank = int(^uint(0) >> 1)
		}
		if leftRank != rightRank {
			return leftRank < rightRank
		}
	}
	leftActivity := projectTopicSortValue(left.CreatedAt, left.LastActivityAt, sortMode)
	rightActivity := projectTopicSortValue(right.CreatedAt, right.LastActivityAt, sortMode)
	if leftActivity != rightActivity {
		return leftActivity > rightActivity
	}
	if left.TopicID != right.TopicID {
		return left.TopicID < right.TopicID
	}
	return left.Key < right.Key
}
