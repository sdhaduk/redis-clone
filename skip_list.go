package main

import (
	"math/rand"
)

const maxHeight int = 32
const probability float64 = 0.25

type node struct {
	member string
	score  float64
	next   []*node
	span   []int64
}

type SkipList struct {
	head     *node
	level    int // starts at level 0 (largest index being used)
	numNodes int
}

func randomLevel() int {
	level := 0
	for rand.Float64() < probability && level < maxHeight-1 {
		level += 1
	}
	return level
}

func NewSkipList() (*SkipList) {
	return &SkipList{head: &node{next: make([]*node, maxHeight), span: make([]int64, maxHeight)}, level: 0, numNodes: 0}
}

func (n *node) precedes(score float64, member string) bool {
	if n.score != score {
		return n.score < score
	}
	return n.member < member
}

func (sl *SkipList) search(score float64, member string) (update [maxHeight]*node, ranks [maxHeight]int64) {
	current := sl.head
	level := sl.level
	var running_count int64 = 0
	for level >= 0 {
		if current.next[level] != nil && current.next[level].precedes(score, member) {
			running_count += current.span[level]
			current = current.next[level]
			continue
		}
		ranks[level] = running_count
		update[level] = current
		level -= 1
	}
	return update, ranks
}

func (sl *SkipList) insert(score float64, member string) {
	prevNodes, ranks := sl.search(score, member)
	newNodeHeight := randomLevel()
	newNode := &node{score: score, member: member, next: make([]*node, newNodeHeight+1), span: make([]int64, newNodeHeight+1)}
	newNode.span[0] = 1

	for i := 0; i < newNodeHeight+1; i++ {
		if i > sl.level {
			sl.level += 1
			sl.head.next[i] = newNode
			sl.head.span[i] = ranks[0] + 1
			newNode.next[i] = nil
			continue
		}

		prevNode := prevNodes[i]
		nextNode := prevNode.next[i]

		if nextNode != nil {
			newNode.span[i] = prevNode.span[i] - (ranks[0] - ranks[i])
		} 
		prevNode.span[i] = (ranks[0] - ranks[i]) + 1

		newNode.next[i] = nextNode
		prevNode.next[i] = newNode
	}

	for i := newNodeHeight + 1; i <= sl.level; i++ {
		prevNodes[i].span[i] += 1
	}	
	sl.numNodes += 1
}

func (sl *SkipList) delete(score float64, member string) bool {
	prevNodes, _ := sl.search(score, member)
	curNode := prevNodes[0].next[0]
	if curNode == nil || (curNode.member != member || curNode.score != score) {
		return false
	}

	for i := 0; i < len(curNode.next); i++ {
		prevNode := prevNodes[i]
		nextNode := curNode.next[i]

		prevNode.next[i] = nextNode
		prevNode.span[i] = prevNode.span[i] + curNode.span[i] - 1
	}

	for i := len(curNode.next); i <= sl.level; i++ {
		prevNodes[i].span[i] -= 1
	}

	for sl.level > 0 && sl.head.next[sl.level] == nil {
		sl.level -= 1
	}
	sl.numNodes -= 1
	return true
}

func (sl *SkipList) oracleRank(score float64, member string) int64 {
	curNode := sl.head.next[0]
	var count int64 = 0

	for curNode != nil {
		if curNode.score == score && curNode.member == member {
			return count
		}
		if curNode.score > score && curNode.member > member {
			return -1
		}
		curNode = curNode.next[0]
		count += 1
	}
	return -1
}
