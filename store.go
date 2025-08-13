// Задача найти все ошибки которые тут есть
// Код представляет собой некое хранилище в памяти
// data - основные данные ключ-значение
// Item - сам элемент со значением ttl и кол-ом просмотров
// lastKeys - стек с максимальным кол-ом элементов 30 который хранит последние ключи
package store

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// Item представляет элемент хранилища с возможностью истечения.
type Item struct {
	Value     string        `json:"value"`
	ExpiresAt time.Time     `json:"expiresAt"` // Если время не задано, считается, что элемент не истекает.
	Views     atomic.Uint64 `json:"views"`     // +new: атомик быстрее и потокобезопаснее, подходит для инкриментов
}

// Store – простое in-memory хранилище.
type Store struct {
	mu   sync.RWMutex
	data map[string]*Item // +new: храним указатель на Item, что-бы работать с оригинальным значением в ресиверах

	//стек последних ключей
	stackMutex sync.Mutex
	lastKeys   []string // последние ключи
}

// NewStore создаёт новое хранилище.
func NewStore() *Store { // +new: возвращаем указатель на наш Стор, который создали
	return &Store{
		lastKeys: make([]string, 0, 30),
		data:     make(map[string]*Item), // +new: нужно инициализировать мапу, что-бы избежать ошибок
	}
}

// Set сохраняет значение по ключу с TTL в секундах.
// Если ttl <= 0, ключ не имеет срока истечения.
// +new: используем указатели на Store, что-бы ставить mutex на оригинальный кеш, и ttl = time.Duration для удобства
// +new: upd. TTL в time.Duration
func (s *Store) Set(key, value string, ttl time.Duration) {
	var expires time.Time
	if ttl > 0 {
		expires = time.Now().Add(ttl)
	}
	s.mu.Lock()          // +new: используем единый мутекс, не создаем новые каждый раз
	s.data[key] = &Item{ // +new: сохраняем указатель на наш новый Итем
		Value:     value,
		ExpiresAt: expires,
	}
	s.mu.Unlock() // +new: сразу отпустили Lock, как сохранили
	s.push(key)
}

// RetrieveLastKey извлекает последний ключ
// удаляет его из мапы и показывает пользователю
// +new: и удаляет последний ключ из стака
func (s *Store) RetrieveLastKey() string {
	s.stackMutex.Lock() // +new: top() и pop() не атомарны - между ними моджет вклинится другой поток
	if len(s.lastKeys) == 0 {
		s.stackMutex.Unlock()
		return ""
	}

	k := s.lastKeys[len(s.lastKeys)-1]
	s.lastKeys = s.lastKeys[:len(s.lastKeys)-1]
	s.stackMutex.Unlock()

	s.mu.Lock()
	delete(s.data, k)
	s.mu.Unlock()

	return k
}

// Size - получаем размер хранилища
func (s *Store) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l := len(s.data) // +new убрал Println, потому что возврат размера не подразумевает вывод к консоль
	return l
}

// Get возвращает значение для ключа, если он существует и не истёк.
func (s *Store) Get(key string) (string, bool) {
	//	+new: if s.Size() == 0 лишняя проверка, потому что на if !ok, все-ровно вернем "", false
	s.mu.RLock()
	item, ok := s.data[key]
	s.mu.RUnlock() // +new: отпустили мутекс на чтение сразу после прочтения

	if !ok {
		return "", false
	}
	// Если у элемента задано время истечения и оно прошло, считаем, что ключ не найден.
	// +new добавил = проверку, на то что итем не удалился, перед проверкой его значения
	if !item.ExpiresAt.IsZero() && time.Now().After(item.ExpiresAt) {
		s.mu.Lock()
		if curValue, ok := s.data[key]; ok && curValue == item {
			delete(s.data, key)
		}

		s.mu.Unlock()
		return "", false
	}
	item.Views.Add(1) // +new: увеличваем количество просмотров на 1

	return item.Value, true
}

// GetViews - вернет сколько просмотрели ключ
func (s *Store) GetViews(key string) uint64 {
	s.mu.RLock()
	item, ok := s.data[key]
	s.mu.RUnlock()

	if !ok {
		return 0
	}
	return item.Views.Load() // +new: возвращаем число просмотров из атомика
}

// Delete удаляет элемент по ключу.
func (s *Store) Delete(key string) {
	s.mu.Lock() // +new: ставим лок из оригинального *Store
	defer s.mu.Unlock()

	delete(s.data, key)
}

// +new: DTO без атомика
type ItemDTO struct {
	Value     string
	ExpiresAt time.Time
	Views     uint64
}

// FullList возвращает список всего
// +new: решил использовать DTO, для того что-бы не отдавать оригинальные значения наружу
// map[string]*Item — нельзя (утечка внутренних указателей).
// map[string]Item — тоже нельзя, потому что это копирует atomic.Uint64
func (s *Store) FullList() map[string]ItemDTO {
	s.mu.RLock()
	newData := make(map[string]ItemDTO, len(s.data)) //	+new: сразу выделяем память

	for key, val := range s.data {
		newValue := ItemDTO{
			Value:     val.Value,
			ExpiresAt: val.ExpiresAt,
			Views:     val.Views.Load(), // +new: сохраняем значение как uint64
		}
		newData[key] = newValue
	}

	s.mu.RUnlock()

	return newData
}

// Cleanup периодически очищает хранилище от просроченных элементов.
// +new: перепишу Cleanup, добавлю отмену по контексту и тикер вместо sleep
func (s *Store) Cleanup(ctx context.Context, cleanTicker *time.Ticker) {
	defer cleanTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-cleanTicker.C:
			expiredKeys := []string{}

			now := time.Now()
			s.mu.RLock() // +new: делаем Rlock, для сбора истекших ключей
			for k, item := range s.data {
				if !item.ExpiresAt.IsZero() && now.After(item.ExpiresAt) {
					expiredKeys = append(expiredKeys, k)
				}
			}

			s.mu.RUnlock()

			if len(expiredKeys) == 0 { // +new: если нет истекших ключей - выходим
				continue
			}

			s.mu.Lock() // +new: ставим лок для удаления всех ключей, которые мы собрали
			for _, v := range expiredKeys {
				delete(s.data, v)
			}
			s.mu.Unlock()
		}
	}
}

// Reset очищает всё хранилище
// +new: добавил очистку ключей из стека тоже
func (s *Store) Reset() {
	s.stackMutex.Lock()
	s.lastKeys = make([]string, 0, 30)
	s.stackMutex.Unlock()

	s.mu.Lock()
	s.data = make(map[string]*Item)
	s.mu.Unlock()

}

// сохраняем элемент
func (s *Store) push(value string) {
	// +new: соблюдаем условие, что в стеке должно быть 30 последних элементов
	s.stackMutex.Lock()

	s.lastKeys = append(s.lastKeys, value)
	if len(s.lastKeys) > 30 {
		s.lastKeys = s.lastKeys[1:]
	}

	s.stackMutex.Unlock()
}

// удаляем верхний элемент
func (s *Store) pop() {
	s.stackMutex.Lock()

	if len(s.lastKeys) == 0 {
		s.stackMutex.Unlock()
		return
	}

	s.lastKeys = s.lastKeys[:len(s.lastKeys)-1]
	s.stackMutex.Unlock()

}

// получаем верхний элемент
func (s *Store) top() string {
	s.stackMutex.Lock()
	defer s.stackMutex.Unlock()

	if len(s.lastKeys) == 0 {
		return ""
	}

	return s.lastKeys[len(s.lastKeys)-1] // +new: берем длину от lastKeys, а не от data
}
