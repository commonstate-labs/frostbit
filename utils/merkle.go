package utils

import "crypto/sha256"

const TxPerMerkleTree = 1000

                                                                                               
func BuildBlockHash(allTxs [][]byte) []byte {
	if len(allTxs) == 0 {
		                                          
		return Sha256Hash([]byte("empty_block"))
	}

	         
	chunks := chunkTransactions(allTxs, TxPerMerkleTree)

	                                                    
	var merkleRoots [][]byte
	for _, c := range chunks {
		root := buildMerkleRoot(c)
		merkleRoots = append(merkleRoots, root)
	}

	                                           
	if len(merkleRoots) == 1 {
		return merkleRoots[0]
	}

	                                                                       
	return buildMerkleRoot(merkleRoots)
}

func chunkTransactions(allTxs [][]byte, chunkSize int) [][][]byte {
	var chunks [][][]byte
	total := len(allTxs)
	for start := 0; start < total; start += chunkSize {
		end := start + chunkSize
		if end > total {
			end = total
		}
		chunk := allTxs[start:end]

		                             
		if len(chunk) < chunkSize {
			lastTx := chunk[len(chunk)-1]
			for len(chunk) < chunkSize {
				chunk = append(chunk, lastTx)
			}
		}
		chunks = append(chunks, chunk)
	}
	return chunks
}

func buildMerkleRoot(leaves [][]byte) []byte {
	if len(leaves) == 1 {
		                                                            
		return Sha256Hash(leaves[0])
	}
	level := hashLeaves(leaves)
	for len(level) > 1 {
		level = buildMerkleLevel(level)
	}
	return level[0]
}

func hashLeaves(data [][]byte) [][]byte {
	hashed := make([][]byte, len(data))
	for i, d := range data {
		hashed[i] = Sha256Hash(d)
	}
	return hashed
}
func sha256Hash(data []byte) []byte {
	sum := sha256.Sum256(data)
	                                        
	return sum[:]
}
func buildMerkleLevel(nodes [][]byte) [][]byte {
	                                                      
	if len(nodes)%2 != 0 {
		nodes = append(nodes, nodes[len(nodes)-1])
	}
	nextLevel := make([][]byte, 0, len(nodes)/2)
	for i := 0; i < len(nodes); i += 2 {
		                                  
		combined := append(nodes[i], nodes[i+1]...)
		h := sha256Hash(combined)
		nextLevel = append(nextLevel, h)
	}
	return nextLevel
}
