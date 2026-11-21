package main

/*
#include <stdlib.h>
*/
import "C"

//export DDCall
func DDCall(method, payload *C.char) *C.char {
	return C.CString(callJSON(C.GoString(method), C.GoString(payload)))
}
