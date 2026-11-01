package b

import "example.com/fx/pkg/a"

type local struct{}

func Sum() int {
	type inner struct{}
	_ = inner{}
	_ = local{}
	return a.Total([]a.Shape{&a.Sq{N: 2}})
}

func init() {}

func init() { _ = Sum() }

func Local(x any) int {
	type sizer interface{ Size() int }
	if s, ok := x.(sizer); ok {
		return s.Size()
	}
	if s, ok := x.(interface{ Len() int }); ok {
		return s.Len()
	}
	return 0
}
