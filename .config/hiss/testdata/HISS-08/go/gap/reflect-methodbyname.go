package p

import "reflect"

// CallByName selects and invokes a method from a runtime string. This is Go's closest
// equivalent to eval: the callee is not known statically. No forbidigo rule covers
// reflect, and internal/hiss has no Go HISS-08 rule at all.
func CallByName(v any, name string) []reflect.Value {
	return reflect.ValueOf(v).MethodByName(name).Call(nil)
}
