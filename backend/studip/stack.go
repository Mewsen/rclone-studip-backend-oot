package studip

import "container/list"

type Stack[T any] struct {
	list *list.List
}

func NewStack[T any]() *Stack[T] {
	return &Stack[T]{list: list.New()}
}

func (s *Stack[T]) Push(v T) {
	s.list.PushBack(v)
}

func (s *Stack[T]) Pop() (T, bool) {
	if s.list.Len() == 0 {
		var zero T
		return zero, false
	}
	elem := s.list.Back()

	return s.list.Remove(elem).(T), true
}

func (s *Stack[T]) Peek() (T, bool) {
	if s.list.Len() == 0 {
		var zero T
		return zero, false
	}
	return s.list.Back().Value.(T), true
}

func (s *Stack[T]) Len() int {
	return s.list.Len()
}

func (s *Stack[T]) IsEmpty() bool {
	return s.list.Len() == 0
}
