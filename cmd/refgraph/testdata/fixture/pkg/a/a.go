package a

import (
	"strconv"

	"example.com/fx/pkg/sub"
)

type Shape interface{ Area() int }

type Named interface {
	Shape
	Name() string
}

type Sq struct{ N int }

func (s *Sq) Area() int { return s.N * s.N }

const (
	Max = 10
	Min = 1
)

var Default = &Sq{N: Max}

type List[T any] struct{ items []T }

func (l *List[T]) Push(x T) { l.items = append(l.items, x) }

func Map[T, U any](xs []T, f func(T) U) []U {
	var out []U
	for _, x := range xs {
		out = append(out, f(x))
	}
	return out
}

func Total(shapes []Shape) int {
	t := 0
	for _, s := range shapes {
		t += s.Area()
	}
	return t
}

func Describe(n Named) string { return n.Name() + strconv.Itoa(n.Area()) }

func UseClosure() func() int {
	return func() int { return Default.Area() + sub.Twice(Max) }
}

func MethodValue() func() int {
	s := new(Sq)
	return s.Area
}

func Generic() []string {
	l := &List[int]{}
	l.Push(Min)
	return Map([]int{1}, strconv.Itoa)
}
