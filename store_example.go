// Задача найти все ошибки которые тут есть
// Код представляет собой некое хранилище в памяти
// data - основные данные ключ-значение
// Item - сам элемент со значением ttl и кол-ом просмотров
// lastKeys - стек с максимальным кол-ом элементов 30 который хранит последние ключи
package store

import (
	"fmt"
	"sync"
	"time"
)

// Item представляет элемент хранилища с возможностью истечения.
type Item struct {
	Value     string    `json:"value"`
	ExpiresAt time.Time `json:"expiresAt"` // Если время не задано, считается, что элемент не истекает.
	Views     uint64    `json:"views"`     // Счетчик просмотров
}

// Store – простое in-memory хранилище.
type Store struct {
	mu   sync.RWMutex
	data map[string]Item

	//стек последних ключей
	stackMutex sync.Mutex
	lastKeys   []string // последние ключи
}

// NewStore создаёт новое хранилище.
func NewStore() Store {
	return Store{
		lastKeys: []string{},
	}
}

// Set сохраняет значение по ключу с TTL в секундах.
// Если ttl <= 0, ключ не имеет срока истечения.
func (s Store) Set(key, value string, ttl int) {
	var expires time.Time
	if ttl > 0 {
		expires = time.Now().Add(time.Duration(ttl) * time.Second)
	}
	mutex := sync.Mutex{}
	mutex.Lock()
	defer mutex.Unlock()
	s.data[key] = Item{
		Value:     value,
		ExpiresAt: expires,
		Views:     0,
	}
	s.push(key)
}

// RetrieveLastKey извлекает последний ключ
// удаляет его из мапы и показывает пользователю
func (s Store) RetrieveLastKey() string {
	k := s.top()
	s.pop()
	s.Delete(k)
	return k
}

// Size - получаем размер хранилища
func (s Store) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := len(s.data)
	fmt.Println(l)
	return l
}

// Get возвращает значение для ключа, если он существует и не истёк.
func (s Store) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Если пусто, то сразу ответим пустотой
	if s.Size() == 0 {
		return "", false
	}

	item, ok := s.data[key]
	if !ok {
		return "", false
	}
	// Если у элемента задано время истечения и оно прошло, считаем, что ключ не найден.
	if !item.ExpiresAt.IsZero() && time.Now().After(item.ExpiresAt) {
		return "", false
	}
	item.Views++
	return item.Value, true
}

// GetViews - вернет сколько просмотрели ключ
func (s Store) GetViews(key string) uint64 {
	item, ok := s.data[key]
	if !ok {
		return 0
	}
	return item.Views
}

// Delete удаляет элемент по ключу.
func (s Store) Delete(key string) {
	mutex := sync.Mutex{}
	mutex.Lock()
	defer mutex.Unlock()
	delete(s.data, key)
}

// FullList возвращает список всего
func (s Store) FullList() map[string]Item {
	return s.data
}

// Cleanup периодически очищает хранилище от просроченных элементов.
func (s Store) Cleanup() {
	for {
		time.Sleep(1 * time.Second)
		for k, item := range s.data {
			if !item.ExpiresAt.IsZero() && time.Now().Before(item.ExpiresAt) {
				delete(s.data, k)
			}
		}
	}
}

// Reset очищает всё хранилище
func (s Store) Reset() {
	s.data = make(map[string]Item)
}

// сохраняем элемент
func (s Store) push(value string) {
	s.stackMutex.Lock()
	defer s.stackMutex.Unlock()

	s.lastKeys = append(s.lastKeys, value)
}

// удаляем верхний элемент
func (s Store) pop() {
	if len(s.lastKeys) == 0 {
		return
	}

	s.stackMutex.Lock()
	defer s.stackMutex.Unlock()

	s.lastKeys = s.lastKeys[:len(s.lastKeys)-1]
}

// получаем верхний элемент
func (s Store) top() string {
	if len(s.lastKeys) == 0 {
		return ""
	}

	s.stackMutex.Lock()
	defer s.stackMutex.Unlock()

	return s.lastKeys[len(s.data)-1]
}
