package kubernetes

// LBService manages LoadBalancer services
type LBService struct {
	*BaseService
}

func (s *LBService) Create(appName string) error {
	panic("not implemented")
}

func (s *LBService) Remove(appName string) error {
	panic("not implemented")
}

func (s *LBService) Update(appName string) error {
	panic("not implemented")
}

func (s *LBService) Swap(appSrc string, appDst string) error {
	panic("not implemented")
}

func (s *LBService) Get(appName string) (map[string]string, error) {
	panic("not implemented")
}
