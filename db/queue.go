package db

type WriteTask struct {
	Key   []byte
	Value []byte
	Op    WriteOp                                       
}

type WriteOp int

const (
	OpSet WriteOp = iota
	OpDelete
)
