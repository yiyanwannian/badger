/*
 * Copyright 2019 Dgraph Labs, Inc. and Contributors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
package badger

import (
	"context"
	"fmt"
	"github.com/pkg/errors"
	"log"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dgraph-io/badger/v3/pb"
)

func TestPublisherDeadlock(t *testing.T) {
	runBadgerTest(t, nil, func(t *testing.T, db *DB) {
		var subWg sync.WaitGroup
		subWg.Add(1)

		var firstUpdate sync.WaitGroup
		firstUpdate.Add(1)

		go func() {
			subWg.Done()
			match := pb.Match{Prefix: []byte("ke"), IgnoreBytes: ""}
			db.Subscribe(context.Background(), func(kvs *pb.KVList) error {
				for _, kv := range kvs.GetKv() {
					log.Printf("%+v\n", string(kv.Value))
				}

				firstUpdate.Done()
				time.Sleep(time.Second * 20)
				return errors.New("sending out the error")
			}, []pb.Match{match})
		}()
		subWg.Wait()
		time.Sleep(time.Second * 1)
		log.Print("sending the first update")
		go db.Update(func(txn *Txn) error {
			e := NewEntry([]byte(fmt.Sprintf("key%d", 0)), []byte(fmt.Sprintf("value%d", 0)))
			return txn.SetEntry(e)
		})

		log.Println("first update send")
		firstUpdate.Wait()
		req := int64(0)
		for i := 1; i < 1110; i++ {
			time.Sleep(time.Millisecond * 10)
			go func(i int) {
				db.Update(func(txn *Txn) error {
					e := NewEntry([]byte(fmt.Sprintf("key%d", i)), []byte(fmt.Sprintf("value%d", i)))
					return txn.SetEntry(e)
				})
				atomic.AddInt64(&req, 1)
			}(i)
		}
		log.Println("all updates are done")
		for {
			log.Println(atomic.LoadInt64(&req))
			if atomic.LoadInt64(&req) == 1109 {
				break
			}
			time.Sleep(time.Second)
		}

		time.Sleep(time.Second * 10)
	})
}

func TestPublisherOrdering(t *testing.T) {
	runBadgerTest(t, nil, func(t *testing.T, db *DB) {
		order := []string{}
		var wg sync.WaitGroup
		wg.Add(1)
		var subWg sync.WaitGroup
		subWg.Add(1)
		go func() {
			subWg.Done()
			updates := 0
			match := pb.Match{Prefix: []byte("ke"), IgnoreBytes: ""}
			err := db.Subscribe(context.Background(), func(kvs *pb.KVList) error {
				updates += len(kvs.GetKv())
				for _, kv := range kvs.GetKv() {
					order = append(order, string(kv.Value))
				}
				if updates == 5 {
					wg.Done()
				}
				return nil
			}, []pb.Match{match})
			if err != nil {
				require.Equal(t, err.Error(), context.Canceled.Error())
			}
		}()
		subWg.Wait()
		for i := 0; i < 5; i++ {
			db.Update(func(txn *Txn) error {
				e := NewEntry([]byte(fmt.Sprintf("key%d", i)), []byte(fmt.Sprintf("value%d", i)))
				return txn.SetEntry(e)
			})
		}
		wg.Wait()
		for i := 0; i < 5; i++ {
			require.Equal(t, fmt.Sprintf("value%d", i), order[i])
		}
	})
}

func TestMultiplePrefix(t *testing.T) {
	runBadgerTest(t, nil, func(t *testing.T, db *DB) {
		var wg sync.WaitGroup
		wg.Add(1)
		var subWg sync.WaitGroup
		subWg.Add(1)
		go func() {
			subWg.Done()
			updates := 0
			match1 := pb.Match{Prefix: []byte("ke"), IgnoreBytes: ""}
			match2 := pb.Match{Prefix: []byte("hel"), IgnoreBytes: ""}
			err := db.Subscribe(context.Background(), func(kvs *pb.KVList) error {
				updates += len(kvs.GetKv())
				for _, kv := range kvs.GetKv() {
					if string(kv.Key) == "key" {
						require.Equal(t, string(kv.Value), "value")
					} else {
						require.Equal(t, string(kv.Value), "badger")
					}
				}
				if updates == 2 {
					wg.Done()
				}
				return nil
			}, []pb.Match{match1, match2})
			if err != nil {
				require.Equal(t, err.Error(), context.Canceled.Error())
			}
		}()
		subWg.Wait()
		db.Update(func(txn *Txn) error {
			return txn.SetEntry(NewEntry([]byte("key"), []byte("value")))
		})
		db.Update(func(txn *Txn) error {
			return txn.SetEntry(NewEntry([]byte("hello"), []byte("badger")))
		})
		wg.Wait()
	})
}
