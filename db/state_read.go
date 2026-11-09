package db

                                              
                                                         
func (manager *Manager) GetState(key string) ([]byte, bool, error) {
	manager.mu.RLock()
	stateStore := manager.stateDB
	manager.mu.RUnlock()
	if stateStore == nil {
		return nil, false, nil
	}
	return stateStore.Get(key)
}
