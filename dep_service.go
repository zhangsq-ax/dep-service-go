package dep_service

import (
	"fmt"
	"github.com/go-resty/resty/v2"
	"github.com/nacos-group/nacos-sdk-go/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/model"
	"github.com/zhangsq-ax/dep-service-go/common"
	"github.com/zhangsq-ax/logs"
	nacos_helper "github.com/zhangsq-ax/nacos-helper-go"
	"github.com/zhangsq-ax/nacos-helper-go/options"
	"go.uber.org/zap"
)

type DepServiceNacosOptions struct {
	Name     string
	Group    string
	Clusters []string
	BasePath string
}

type DepServiceStaticOptions struct {
	EnableTLS  bool
	DomainName string
	BasePath   string
	Headers    map[string]string
}

type DepServiceOptions struct {
	Static *DepServiceStaticOptions
	Nacos  *DepServiceNacosOptions
}

type DepService struct {
	opts                        *DepServiceOptions
	nacosServiceInstance        *model.Instance
	nacosClient                 *naming_client.INamingClient
	nacosServiceChangedHandlers []func()
}

func NewDepService(opts *DepServiceOptions) (*DepService, error) {
	service := &DepService{
		opts:                        opts,
		nacosServiceChangedHandlers: make([]func(), 0),
	}
	if opts.Static == nil && opts.Nacos != nil {
		client, err := nacos_helper.GetNamingClient(nil)
		if err != nil {
			return nil, err
		}
		service.nacosClient = client

		err = service.updateNacosServiceInstance()
		if err != nil {
			return nil, err
		}

		err = nacos_helper.SubscribeServiceInstance(client, &options.SubscribeServiceInstanceOptions{
			Clusters:          opts.Nacos.Clusters,
			ServiceName:       opts.Nacos.Name,
			GroupName:         opts.Nacos.Group,
			SubscribeCallback: service.onServiceInstanceChanged,
		})
		if err != nil {
			return nil, err
		}
	}

	return service, nil
}

func (srv *DepService) GenerateRequestUrl(path string, scheme ...string) string {
	s := "http"
	if len(scheme) > 0 {
		s = scheme[0]
	}
	return fmt.Sprintf("%s%s", srv.BaseUrl(s), path)
}

func (srv *DepService) GetRequestHeaders() map[string]string {
	if srv.opts.Static != nil && srv.opts.Static.Headers != nil {
		return srv.opts.Static.Headers
	}
	return make(map[string]string)
}

func (srv *DepService) OnServiceChanged(handler func()) {
	srv.nacosServiceChangedHandlers = append(srv.nacosServiceChangedHandlers, handler)
}

func (srv *DepService) NewHttpRequest() *resty.Request {
	return common.GetRestyClient(srv.BaseUrl("http")).R().SetHeaders(srv.GetRequestHeaders())
}

func (srv *DepService) updateNacosServiceInstance() error {
	if srv.opts.Nacos == nil {
		return nil
	}
	nacosConf := srv.opts.Nacos
	instance, err := nacos_helper.SelectServiceInstance(srv.nacosClient, &options.SelectServiceInstanceOptions{
		ServiceName: nacosConf.Name,
		Clusters:    nacosConf.Clusters,
		GroupName:   nacosConf.Group,
	})
	if err != nil {
		return err
	}
	srv.nacosServiceInstance = instance
	return nil
}

func (srv *DepService) onServiceInstanceChanged(instance *model.Instance, err error) {
	logs.Infow("service-instance-changed", zap.Reflect("instance", instance), zap.Error(err))
	if err == nil {
		srv.nacosServiceInstance = instance
		for _, handler := range srv.nacosServiceChangedHandlers {
			go handler()
		}
	}
}

func (srv *DepService) BaseUrl(scheme string) string {
	if srv.opts.Static != nil {
		if srv.opts.Static.EnableTLS && (scheme == "http" || scheme == "ws") {
			scheme = scheme + "s"
		}
		return fmt.Sprintf("%s://%s%s", scheme, srv.opts.Static.DomainName, srv.opts.Static.BasePath)
	} else if srv.opts.Nacos != nil && srv.nacosServiceInstance != nil {
		return fmt.Sprintf("%s://%s:%d%s", scheme, srv.nacosServiceInstance.Ip, srv.nacosServiceInstance.Port, srv.opts.Nacos.BasePath)
	} else {
		return ""
	}
}
