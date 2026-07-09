package uploadsession

// Test helpers for simulating legacy session residue.

func (s *Store) PutForTest(session Session) error {
	return s.put(session)
}

func (s *Store) GetRawForTest(id string) (Session, error) {
	return s.get(id)
}
