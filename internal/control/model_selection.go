package control

// modelSelection is the connection this controller runs as one value: the ref
// and the identity of the connection it was resolved against always change
// together, and a restart must restore both or neither.
type modelSelection struct {
	ref      string
	identity string
}
