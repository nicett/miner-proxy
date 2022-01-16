package status

import (
	"fmt"
	"miner-proxy/pkg"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/liushuochen/gotable"
	"github.com/wxpusher/wxpusher-sdk-go/model"
)

var (
	totalSzie       int64
	status          sync.Map
	lastonlineCount int64
	startTime       = time.Now()
	pushers         sync.Map
	m               sync.Mutex
)

type Status struct {
	Ip            string    `json:"ip"`
	Status        bool      `json:"status"`
	StopTime      time.Time `json:"stop_time"`
	Size          int64     `json:"size"`
	ConnTime      time.Time `json:"conn_time"`
	RemoteAddress string    `json:"remote_address"`
}

func init() {
	go func() {
		for {
			pushers.Range(func(key, value interface{}) bool {
				p, ok := value.(*pusher)
				if !ok {
					return true
				}
				if err := p.UpdateUsers(); err != nil {
					pkg.Error("更新订阅用户失败: %s", err)
					return true
				}
				return true
			})
			time.Sleep(time.Second*5*60 + 30) // 每5分30秒执行一次
		}
	}()
}

func Show(offlineTime time.Duration) {
	table, _ := gotable.Create("Ip", "传输数据大小", "连接时长", "矿池")
	var result []map[string]string
	var total int64
	now := time.Now()
	var (
		offlineIps     []string
		nowOnlineCount int64
	)

	status.Range(func(key, value interface{}) bool {
		s := value.(*Status)
		if !s.Status && time.Since(s.StopTime).Minutes() >= offlineTime.Minutes() { // 掉线1分钟后, 提醒

			offlineIps = append(offlineIps, s.Ip)
			return true
		}

		if !s.Status {
			return true
		}
		nowOnlineCount++
		total += s.Size
		result = append(result, map[string]string{
			"Ip":     s.Ip,
			"传输数据大小": humanize.Bytes(uint64(s.Size)),
			"连接时长":   now.Sub(s.ConnTime).String(),
			"矿池":     s.RemoteAddress,
		})
		return true
	})
	result = append(result, map[string]string{
		"Ip":     "总计",
		"传输数据大小": humanize.Bytes(uint64(atomic.LoadInt64(&totalSzie))),
		"连接时长":   now.Sub(startTime).String(),
		"矿池":     "",
	})
	table.AddRows(result)
	fmt.Println(table.String())
	// 删除这些过期的ip
	for _, v := range offlineIps {
		status.Delete(v)
	}

	if nowOnlineCount < lastonlineCount { // 掉线通知
		go SendOfflineIps(offlineIps)
		if len(offlineIps) != 0 {
			lastonlineCount = nowOnlineCount
		}
	} else {
		lastonlineCount = nowOnlineCount
	}
}

func SendOfflineIps(offlineIps []string) {
	if len(offlineIps) <= 0 {
		return
	}
	var ips = strings.Join(offlineIps, "\n")
	if len(offlineIps) > 10 {
		ips = fmt.Sprintf("%s 等 %d个ip", strings.Join(offlineIps[:10], "\n"), len(offlineIps))
	}
	ips = fmt.Sprintf("您有掉线的矿机:\n%s", ips)
	pushers.Range(func(key, value interface{}) bool {
		p := value.(*pusher)
		pkg.Debug("发送掉线通知: %+v", p.Users)
		if err := p.SendMessage2All(ips); err != nil {
			pkg.Error("发送通知失败: %s", err)
		}
		return true
	})
}

func Add(ip string, size int64, remoteAddress string) {
	v, _ := status.LoadOrStore(ip, &Status{Ip: ip, ConnTime: time.Now(), RemoteAddress: remoteAddress, Status: true})
	obj := v.(*Status)
	obj.RemoteAddress = remoteAddress
	obj.Size += size
	atomic.AddInt64(&totalSzie, size)
	status.Store(ip, obj)
}

func Del(ip string) {
	v, ok := status.Load(ip)
	if !ok {
		return
	}
	s := v.(*Status)
	s.Status = false
	s.StopTime = time.Now()
	status.Store(ip, s)
}

type Pusher interface {
	SendMessage(text string, uid ...string) error
	GetAllUser() ([]model.WxUser, error)
	GetToken() string
}

type pusher struct {
	Pusher         Pusher
	Users          []model.WxUser
	m              sync.Mutex
	lastUpdateUser time.Time
}

func (p *pusher) UpdateUsers() error {
	if !p.lastUpdateUser.IsZero() && time.Since(p.lastUpdateUser).Minutes() < 5 {
		return nil
	}
	users, _ := p.Pusher.GetAllUser()
	if len(users) == 0 {
		return nil
	}
	m.Lock()
	defer m.Unlock()
	p.Users = users
	p.lastUpdateUser = time.Now()
	return nil
}

func (p *pusher) SendMessage2All(msg string) error {
	var uids []string
	for _, v := range p.Users {
		uids = append(uids, v.UId)
	}
	return p.Pusher.SendMessage(msg, uids...)
}

func AddConnectErrorCallback(p Pusher) error {
	obj := &pusher{
		Pusher: p,
	}
	if err := obj.UpdateUsers(); err != nil {
		return err
	}
	pushers.Store(obj.Pusher.GetToken(), obj)
	return nil
}
