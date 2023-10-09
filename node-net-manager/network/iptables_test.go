package network

type mockiptable struct {
	CalledWith []string
}

func (t *mockiptable) Append(s string, s2 string, s3 ...string) error {
	t.CalledWith = append([]string{s, s2}, s3...)
	return nil
}

func (t *mockiptable) AppendUnique(s string, s2 string, s3 ...string) error {
	//TODO implement me
	panic("implement me")
}

func (t *mockiptable) Delete(s string, s2 string, s3 ...string) error {
	//TODO implement me
	panic("implement me")
}

func (t *mockiptable) DeleteChain(s string, s2 string) error {
	//TODO implement me
	panic("implement me")
}

func (t *mockiptable) AddChain(s string, s2 string) error {
	//TODO implement me
	panic("implement me")
}
