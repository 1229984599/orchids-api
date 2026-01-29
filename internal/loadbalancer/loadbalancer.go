package loadbalancer

import (
	"errors"
	"log"
	"math/rand"
	"sort"
	"sync"
	"time"

	"orchids-api/internal/store"
)

// 账号缓存刷新间隔（从 5 秒改为 30 秒）
const accountsCacheTTL = 30 * time.Second

// 请求计数批量更新间隔（从 5 秒改为 10 秒）
const countUpdateInterval = 10 * time.Second

type LoadBalancer struct {
	store *store.Store

	// 账号缓存
	accounts    []*store.Account
	accountsMu  sync.RWMutex
	lastRefresh time.Time

	// 异步请求计数更新
	pendingUpdates  map[int64]int64
	pendingSuccess  map[int64]int64
	pendingFailure  map[int64]int64
	updateMu        sync.Mutex
	stopChan        chan struct{}
	wg              sync.WaitGroup
}

func New(s *store.Store) *LoadBalancer {
	lb := &LoadBalancer{
		store:          s,
		pendingUpdates: make(map[int64]int64),
		pendingSuccess: make(map[int64]int64),
		pendingFailure: make(map[int64]int64),
		stopChan:       make(chan struct{}),
	}

	// 立即加载账号列表
	lb.refreshAccounts()

	// 启动后台任务
	lb.wg.Add(2)
	go lb.backgroundRefreshAccounts()
	go lb.backgroundUpdateCounts()

	log.Println("[LoadBalancer] 已启动，账号缓存TTL=", accountsCacheTTL, ", 计数更新间隔=", countUpdateInterval)

	return lb
}

// Close 关闭负载均衡器，停止后台任务
func (lb *LoadBalancer) Close() {
	close(lb.stopChan)
	lb.wg.Wait()
	// 最后一次刷新计数
	lb.flushPendingUpdates()
	log.Println("[LoadBalancer] 已关闭")
}

// refreshAccounts 刷新账号缓存
func (lb *LoadBalancer) refreshAccounts() {
	accounts, err := lb.store.GetEnabledAccounts()
	if err != nil {
		log.Printf("[LoadBalancer] 刷新账号失败: %v", err)
		return
	}

	lb.accountsMu.Lock()
	lb.accounts = accounts
	lb.lastRefresh = time.Now()
	lb.accountsMu.Unlock()

	log.Printf("[LoadBalancer] 账号缓存已刷新: %d 个可用账号", len(accounts))
}

// backgroundRefreshAccounts 后台定期刷新账号列表
func (lb *LoadBalancer) backgroundRefreshAccounts() {
	defer lb.wg.Done()
	ticker := time.NewTicker(accountsCacheTTL)
	defer ticker.Stop()

	for {
		select {
		case <-lb.stopChan:
			return
		case <-ticker.C:
			lb.refreshAccounts()
		}
	}
}

// backgroundUpdateCounts 后台批量更新请求计数
func (lb *LoadBalancer) backgroundUpdateCounts() {
	defer lb.wg.Done()
	ticker := time.NewTicker(countUpdateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-lb.stopChan:
			return
		case <-ticker.C:
			lb.flushPendingUpdates()
		}
	}
}

// flushPendingUpdates 将待更新的请求计数写入数据库
func (lb *LoadBalancer) flushPendingUpdates() {
	lb.updateMu.Lock()
	if len(lb.pendingUpdates) == 0 && len(lb.pendingSuccess) == 0 && len(lb.pendingFailure) == 0 {
		lb.updateMu.Unlock()
		return
	}
	updates := lb.pendingUpdates
	successUpdates := lb.pendingSuccess
	failureUpdates := lb.pendingFailure
	lb.pendingUpdates = make(map[int64]int64)
	lb.pendingSuccess = make(map[int64]int64)
	lb.pendingFailure = make(map[int64]int64)
	lb.updateMu.Unlock()

	// 更新请求计数
	for accountID, count := range updates {
		if err := lb.store.AddRequestCount(accountID, count); err != nil {
			log.Printf("[LoadBalancer] 更新请求计数失败: accountID=%d, count=%d, err=%v", accountID, count, err)
		}
	}

	// 更新成功计数
	for accountID, count := range successUpdates {
		if err := lb.store.AddSuccessCount(accountID, count); err != nil {
			log.Printf("[LoadBalancer] 更新成功计数失败: accountID=%d, count=%d, err=%v", accountID, count, err)
		}
	}

	// 更新失败计数
	for accountID, count := range failureUpdates {
		if err := lb.store.AddFailureCount(accountID, count); err != nil {
			log.Printf("[LoadBalancer] 更新失败计数失败: accountID=%d, count=%d, err=%v", accountID, count, err)
		}
	}
}

// scheduleCountUpdate 调度请求计数更新（异步）
func (lb *LoadBalancer) scheduleCountUpdate(accountID int64) {
	lb.updateMu.Lock()
	lb.pendingUpdates[accountID]++
	lb.updateMu.Unlock()
}

// ScheduleSuccessCount 调度成功计数更新（异步）
func (lb *LoadBalancer) ScheduleSuccessCount(accountID int64) {
	lb.updateMu.Lock()
	lb.pendingSuccess[accountID]++
	lb.updateMu.Unlock()
}

// ScheduleFailureCount 调度失败计数更新（异步）
func (lb *LoadBalancer) ScheduleFailureCount(accountID int64) {
	lb.updateMu.Lock()
	lb.pendingFailure[accountID]++
	lb.updateMu.Unlock()
}

// getCachedAccounts 获取缓存的账号列表（如果缓存过期则刷新）
func (lb *LoadBalancer) getCachedAccounts() []*store.Account {
	lb.accountsMu.RLock()
	accounts := lb.accounts
	lastRefresh := lb.lastRefresh
	lb.accountsMu.RUnlock()

	// 如果缓存为空或过期，同步刷新
	if len(accounts) == 0 || time.Since(lastRefresh) > accountsCacheTTL*2 {
		lb.refreshAccounts()
		lb.accountsMu.RLock()
		accounts = lb.accounts
		lb.accountsMu.RUnlock()
	}

	return accounts
}

func (lb *LoadBalancer) GetNextAccount() (*store.Account, error) {
	return lb.GetNextAccountExcluding(nil)
}

func (lb *LoadBalancer) GetNextAccountExcluding(excludeIDs []int64) (*store.Account, error) {
	// 从缓存获取账号列表（无锁读取）
	accounts := lb.getCachedAccounts()

	// 过滤排除的账号
	if len(excludeIDs) > 0 {
		excludeSet := make(map[int64]bool)
		for _, id := range excludeIDs {
			excludeSet[id] = true
		}
		var filtered []*store.Account
		for _, acc := range accounts {
			if !excludeSet[acc.ID] {
				filtered = append(filtered, acc)
			}
		}
		accounts = filtered
	}

	if len(accounts) == 0 {
		return nil, errors.New("no enabled accounts available")
	}

	// 选择账号
	account := lb.selectAccount(accounts)

	// 异步更新请求计数（不阻塞请求处理）
	lb.scheduleCountUpdate(account.ID)

	return account, nil
}

// selectAccount 使用前缀和 + 二分查找选择账号（O(log n)）
func (lb *LoadBalancer) selectAccount(accounts []*store.Account) *store.Account {
	if len(accounts) == 1 {
		return accounts[0]
	}

	// 构建前缀和数组
	prefixSum := make([]int, len(accounts)+1)
	for i, acc := range accounts {
		prefixSum[i+1] = prefixSum[i] + acc.Weight
	}

	totalWeight := prefixSum[len(accounts)]
	if totalWeight == 0 {
		return accounts[0]
	}

	randomWeight := rand.Intn(totalWeight)

	// 二分查找：找到第一个 prefixSum[i+1] > randomWeight 的 i
	idx := sort.Search(len(accounts), func(i int) bool {
		return prefixSum[i+1] > randomWeight
	})

	if idx >= len(accounts) {
		idx = len(accounts) - 1
	}

	return accounts[idx]
}

// ForceRefresh 强制刷新账号缓存（用于账号变更后）
func (lb *LoadBalancer) ForceRefresh() {
	lb.refreshAccounts()
}
