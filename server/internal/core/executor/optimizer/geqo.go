package optimizer

import (
	"math/rand"
	"vaultdb/internal/core/parser"
)

// GEQO implementation for large join graphs
func (o *Optimizer) applyGEQO(dbName string, plan *OptimizedPlan, g *JoinGraph) *JoinTree {
	n := len(g.Relations)
	popSize := 20
	generations := 50

	// Initialize population
	population := make([][]int, popSize)
	for i := 0; i < popSize; i++ {
		population[i] = randomPermutation(n)
	}

	bestCost := -1.0
	var bestTree *JoinTree

	for gen := 0; gen < generations; gen++ {
		// Evaluate population
		costs := make([]float64, popSize)
		trees := make([]*JoinTree, popSize)
		for i := 0; i < popSize; i++ {
			t, c := o.evaluatePermutation(dbName, plan, g, population[i])
			costs[i] = c
			trees[i] = t
			if bestCost == -1.0 || c < bestCost {
				bestCost = c
				bestTree = t
			}
		}

		// Selection (tournament)
		newPopulation := make([][]int, popSize)
		newPopulation[0] = population[minIndex(costs)] // elitism
		for i := 1; i < popSize; i++ {
			p1 := population[rand.Intn(popSize)]
			p2 := population[rand.Intn(popSize)]
			// edge recombination crossover (simplified as order crossover)
			newPopulation[i] = orderCrossover(p1, p2)
			// mutate
			if rand.Float64() < 0.1 {
				idx1 := rand.Intn(n)
				idx2 := rand.Intn(n)
				newPopulation[i][idx1], newPopulation[i][idx2] = newPopulation[i][idx2], newPopulation[i][idx1]
			}
		}
		population = newPopulation
	}

	return bestTree
}

func randomPermutation(n int) []int {
	p := make([]int, n)
	for i := 0; i < n; i++ {
		p[i] = i
	}
	rand.Shuffle(n, func(i, j int) { p[i], p[j] = p[j], p[i] })
	return p
}

func minIndex(arr []float64) int {
	minIdx := 0
	for i := 1; i < len(arr); i++ {
		if arr[i] < arr[minIdx] {
			minIdx = i
		}
	}
	return minIdx
}

func orderCrossover(p1, p2 []int) []int {
	n := len(p1)
	child := make([]int, n)
	for i := 0; i < n; i++ {
		child[i] = -1
	}

	start := rand.Intn(n)
	end := rand.Intn(n)
	if start > end {
		start, end = end, start
	}

	for i := start; i <= end; i++ {
		child[i] = p1[i]
	}

	p2Idx := 0
	for i := 0; i < n; i++ {
		if child[i] == -1 {
			for contains(child, p2[p2Idx]) {
				p2Idx++
			}
			child[i] = p2[p2Idx]
		}
	}
	return child
}

func contains(arr []int, val int) bool {
	for _, v := range arr {
		if v == val {
			return true
		}
	}
	return false
}

func (o *Optimizer) evaluatePermutation(dbName string, plan *OptimizedPlan, g *JoinGraph, perm []int) (*JoinTree, float64) {
	if len(perm) == 0 {
		return nil, 0
	}

	ts := o.stats.GetTableStats(dbName, g.Relations[perm[0]])
	rows := o.effectiveRowCount(dbName, g.Relations[perm[0]], ts, plan)

	tree := &JoinTree{
		Type:      BaseRelation,
		TableName: g.Relations[perm[0]],
		Alias:     g.Aliases[perm[0]],
		Rows:      rows,
		Cost:      0,
	}

	for i := 1; i < len(perm); i++ {
		ts2 := o.stats.GetTableStats(dbName, g.Relations[perm[i]])
		rows2 := o.effectiveRowCount(dbName, g.Relations[perm[i]], ts2, plan)
		rightTree := &JoinTree{
			Type:      BaseRelation,
			TableName: g.Relations[perm[i]],
			Alias:     g.Aliases[perm[i]],
			Rows:      rows2,
			Cost:      0,
		}

		// simplified join cond evaluation
		var matchCond parser.Expression
		for _, cond := range g.Predicates {
			matchCond = cond
			break
		}

		tree = o.evaluateJoin(tree, rightTree, []parser.Expression{matchCond})
		tree.JoinCond = matchCond
	}

	return tree, tree.Cost
}
