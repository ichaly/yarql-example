package main

import (
	"context"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/mjarkk/yarql"
	"github.com/yckao/go-dataloader"
	"io/ioutil"
	"mime/multipart"
	"sync"
)

var (
	users = map[int]User{
		1: {ID: 1, Name: "Elon Musk"},
		2: {ID: 2, Name: "Steve jobs"},
		3: {ID: 3, Name: "Bill Gates"},
	}
	friends = map[int]int{
		1: 2,
		2: 3,
		3: 1,
	}
	userLoader dataloader.DataLoader[int, User, int]
)

func batchLoadFn(ctx context.Context, keys []int) []dataloader.Result[User] {
	fmt.Println("batchLoadFn")
	result := make([]dataloader.Result[User], len(keys))
	for index, key := range keys {
		friend := users[friends[key]]
		result[index] = dataloader.Result[User]{
			Value: friend,
		}
	}
	return result
}

type User struct {
	ID   int
	Name string
}

func (my User) ResolveFriends(ctx *yarql.Ctx) []User {
	fmt.Println("ResolveFriends")
	thunk := userLoader.Load(ctx.GetContext(), my.ID)
	//The loader is asynchronous but the fetch is synchronous
	//get will wait until thunk value or error is set
	//So the loader doesn't work for avoid N+1
	val, _ := thunk.Get(ctx.GetContext())
	return []User{val}
}

type QueryRoot struct{}

type MethodRoot struct{}

func (my QueryRoot) ResolveUsers() ([]User, error) {
	s := make([]User, 0, len(users))
	for _, v := range users {
		s = append(s, v)
	}
	return s, nil
}

func (my MethodRoot) ResolveSignUp(ctx *yarql.Ctx, args struct {
	ID   int
	Name string
}) (User, error) {
	user := User{ID: args.ID, Name: args.Name}
	users[args.ID] = user
	return user, nil
}

func main() {
	r := gin.Default()
	graphqlSchema := yarql.NewSchema()

	err := graphqlSchema.Parse(QueryRoot{}, MethodRoot{}, nil)
	if err != nil {
		panic(err)
	}
	// The GraphQL is not thread safe so we use this lock to prevent race conditions and other errors
	var lock sync.Mutex
	r.Any("/graphql", func(c *gin.Context) {
		userLoader = dataloader.New[int, User, int](c, batchLoadFn)
		var form *multipart.Form
		getForm := func() (*multipart.Form, error) {
			if form != nil {
				return form, nil
			}
			var err error
			form, err = c.MultipartForm()
			return form, err
		}
		lock.Lock()
		defer lock.Unlock()
		res, _ := graphqlSchema.HandleRequest(
			c.Request.Method,
			c.Query,
			func(key string) (string, error) {
				form, err := getForm()
				if err != nil {
					return "", err
				}
				values, ok := form.Value[key]
				if !ok || len(values) == 0 {
					return "", nil
				}
				return values[0], nil
			},
			func() []byte {
				requestBody, _ := ioutil.ReadAll(c.Request.Body)
				return requestBody
			},
			c.ContentType(),
			&yarql.RequestOptions{
				Context: c,
				GetFormFile: func(key string) (*multipart.FileHeader, error) {
					form, err := getForm()
					if err != nil {
						return nil, err
					}
					files, ok := form.File[key]
					if !ok || len(files) == 0 {
						return nil, nil
					}
					return files[0], nil
				},
				Tracing: false,
			},
		)
		c.Data(200, "application/json", res)
	})
	r.Run()
}
