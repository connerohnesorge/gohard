package gen

import "fmt"

func emit(m map[string]int) {
	for k, v := range m { // want `range over a map whose body depends on`
		fmt.Println(k, v)
	}
}
