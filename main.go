package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"os"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go"
	"firebase.google.com/go/auth"
	"github.com/gin-gonic/contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

type UserAuth struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type DishCreateRequest struct {
	Name     string `json:"name"`
	Category string `json:"category"`
	Points   int    `json:"points"`
	Date     string `json:"date"`
}

type DishGetRequestParams struct {
	AuthorEmail string `json:"authoremail"`
	Date        string `json:"date"`
}

type Dish struct {
	Id          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Points      int    `json:"points"`
	AuthorEmail string `json:"authoremail"`
	Date        string `json:"date"`
}

func main() {
	godotenv.Load(".env")
	sesh_key, KeyOk := os.LookupEnv("SESSION_KEY")
	sesh_secret, SecretOk := os.LookupEnv("SESSION_SECRET")
	if !KeyOk || !SecretOk {
		return
	}

	ctx := context.Background()
	// Use a service account
	// Read env vars for service account
	serviceAccountJson := []byte(`{
		"type": "service_account",
		"project_id": "` + os.Getenv("PROJECT_ID") + `",
		"private_key_id": "` + os.Getenv("PRIVATE_KEY_ID") + `",
		"private_key": "` + os.Getenv("PRIVATE_KEY") + `",
		"client_email": "` + os.Getenv("CLIENT_EMAIL") + `",
		"client_id": "` + os.Getenv("CLIENT_ID") + `",
		"auth_uri": "` + os.Getenv("AUTH_URI") + `",
		"token_uri": "` + os.Getenv("TOKEN_URI") + `",
		"auth_provider_x509_cert_url": "` + os.Getenv("AUTH_PROVIDER_X509_CERT_URL") + `",
		"client_x509_cert_url": "` + os.Getenv("CLIENT_X509_CERT_URL") + `"
	  }`)
	sa := option.WithCredentialsJSON(serviceAccountJson)
	app, err := firebase.NewApp(ctx, nil, sa)
	if err != nil {
		log.Fatalln(err)
	}

	// Create a client instance for Firestore
	client, err := app.Firestore(ctx)
	if err != nil {
		log.Fatalln(err)
	}
	defer client.Close()

	// Get an auth client from the firebase.App
	authClient, authClientErr := CreateFirebaseAuthClient(ctx, app)
	if authClientErr != nil {
		log.Fatal(authClientErr)
	}

	// Create Google Cloud Storage Client
	// storageClient, err := storage.NewClient(ctx, sa)
	// if err != nil {
	// 	log.Fatal(err)
	// }

	// Set up gin router
	r := gin.Default()

	// Sessions を使用する宣言
	r.Use(sessions.Sessions(sesh_key, sessions.NewCookieStore([]byte(sesh_secret))))

	// CSS などの static files
	r.Static("/static", "./views/static")
	// Load HTML files in views
	r.LoadHTMLGlob("views/*.html")

	api := r.Group("/api")
	api.Use()
	{
		// Signup api
		api.POST("/signup", func(c *gin.Context) {
			UserSignup(c, authClient, client, ctx)
		})

		// Signin api
		api.POST("/signin", func(c *gin.Context) {
			UserSignin(c, authClient, ctx)
		})

		api.GET("/is-logged-in", func(c *gin.Context) {
			loginUserEmail, err := FindUserEmailFromSession(c, authClient, ctx)
			if err != nil {
				c.JSON(http.StatusOK, gin.H{
					"message":          "Not signed in.",
					"currentUserEmail": "",
				})
				return
			}
			c.JSON(http.StatusOK, gin.H{
				"message":          "You are signed in.",
				"currentUserEmail": loginUserEmail,
			})
		})

		api.GET("/dish", func(c *gin.Context) {
			var json DishGetRequestParams
			json.AuthorEmail = c.Query("authoremail")
			json.Date = c.Query("date")

			dishes, err := GetDishes(client, ctx, json)
			if err == nil {
				c.JSON(http.StatusOK, gin.H{
					"value": dishes,
				})
				return
			}
			c.JSON(http.StatusOK, gin.H{
				"message": "error",
			})
		})

		api.POST("/dish", func(c *gin.Context) {
			CreateDish(c, authClient, client, ctx)
		})

		api.GET("/signout", UserSignout)
	}

	r.GET("/signup", func(c *gin.Context) {
		c.HTML(http.StatusOK, "signup.html", gin.H{})
	})

	r.GET("/signin", func(c *gin.Context) {
		c.HTML(http.StatusOK, "signin.html", gin.H{})
	})

	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "add-dish.html", gin.H{})
	})

	r.GET("/add-dish", func(c *gin.Context) {
		c.HTML(http.StatusOK, "add-dish.html", gin.H{})
	})

	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{
			"message": "not found",
		})
	})

	r.Run() // listen and serve on 0.0.0.0:8080 (for windows "localhost:8080")
}

func CreateFirebaseAuthClient(ctx context.Context, app *firebase.App) (*auth.Client, error) {
	client, err := app.Auth(ctx)
	return client, err
}

func UserSignup(c *gin.Context, authClient *auth.Client, client *firestore.Client, ctx context.Context) {
	// バリデーション処理
	var json UserAuth
	if err := c.ShouldBindJSON(&json); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Add Function to check the Email is actually email (using regex)
	_, err := mail.ParseAddress(json.Email)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Create user with Google Auth
	params := (&auth.UserToCreate{}).
		Email(json.Email).
		Password(json.Password)
	u, createUserErr := authClient.CreateUser(ctx, params)
	if createUserErr != nil {
		log.Fatalf("error creating user: %v\n", createUserErr)
		c.JSON(http.StatusBadRequest, gin.H{"error": createUserErr.Error()})
		return
	}
	log.Printf("Successfully created user: %v\n", u)

	// Create user in Firebase
	if err := CreateUser(client, ctx, u.UID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "successful",
	})
}

func CreateUser(client *firestore.Client, ctx context.Context, uid string) error {
	fmt.Printf("Create user with uid: %s\n", uid)

	_, _, errInsert := client.Collection("users").Add(ctx, map[string]interface{}{
		"uid":     uid,
		"follows": []string{},
	})
	if errInsert != nil {
		log.Fatalf("Failed adding: %v", errInsert)
	}

	return nil
}

func UserSignin(c *gin.Context, authClient *auth.Client, ctx context.Context) {
	// バリデーション処理
	var json UserAuth
	if err := c.ShouldBindJSON(&json); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get user by email from Google Auth
	u, getUserErr := authClient.GetUserByEmail(ctx, json.Email)
	if getUserErr != nil {
		log.Fatalf("error getting user by email %s: %v\n", json.Email, getUserErr)
		c.JSON(http.StatusBadRequest, gin.H{"error": getUserErr.Error()})
		return
	}
	log.Printf("Successfully fetched user data: %v\n", u.UID)
	fmt.Println("ログインできました: ", u.Email)
	session := sessions.Default(c)
	session.Set("gin_session_user_email", u.Email)

	// c.SetCookie("gin_cookie_username", user.Email, 3600, "/", "localhost", false, true)

	if err := session.Save(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save session"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "successful",
	})
}

func UserSignout(c *gin.Context) {
	session := sessions.Default(c)
	log.Print("Retrieved Session")
	session.Clear()
	log.Print("Cleared Session")
	session.Save()
	log.Print("Saved Empty Session, Redirecting to top page...")
	c.Redirect(http.StatusFound, "/")
}

func FindUserEmailFromSession(c *gin.Context, authClient *auth.Client, ctx context.Context) (string, error) {
	session := sessions.Default(c)
	userEmail := session.Get("gin_session_user_email")
	if userEmail == nil {
		return "", fmt.Errorf("session is nil")
	}
	// user, userFindErr := FindUserByEmail(client, ctx, userEmail.(string))
	u, getUserErr := authClient.GetUserByEmail(ctx, userEmail.(string))
	if getUserErr != nil {
		log.Fatalf("error getting user by email %s: %v\n", userEmail, getUserErr)
		c.JSON(http.StatusBadRequest, gin.H{"error": getUserErr.Error()})
		return "", getUserErr
	}
	return u.Email, getUserErr
}

func FindUserUidFromSession(c *gin.Context, authClient *auth.Client, ctx context.Context) (string, error) {
	session := sessions.Default(c)
	userEmail := session.Get("gin_session_user_email")
	if userEmail == nil {
		return "", fmt.Errorf("session is nil")
	}
	// user, userFindErr := FindUserByEmail(client, ctx, userEmail.(string))
	u, getUserErr := authClient.GetUserByEmail(ctx, userEmail.(string))
	if getUserErr != nil {
		log.Fatalf("error getting user by email %s: %v\n", userEmail, getUserErr)
		c.JSON(http.StatusBadRequest, gin.H{"error": getUserErr.Error()})
		return "", getUserErr
	}
	return u.UID, getUserErr
}

func CreateDish(c *gin.Context, authClient *auth.Client, client *firestore.Client, ctx context.Context) error {
	var json DishCreateRequest
	if err := c.ShouldBindJSON(&json); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil
	}

	sessionUserEmail, sessionUserErr := FindUserEmailFromSession(c, authClient, ctx)
	if sessionUserErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": sessionUserErr.Error()})
		return nil
	}

	userUid, uidErr := FindUserUidFromSession(c, authClient, ctx)
	if uidErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": uidErr.Error()})
	}

	fmt.Printf("Create dish with name: %s author email: %s\n", json.Name, sessionUserEmail)

	_, _, errInsert := client.Collection("dishes").Add(ctx, map[string]interface{}{
		"name":        json.Name,
		"UserId":      userUid,
		"Category":    json.Category,
		"Points":      json.Points,
		"authorEmail": sessionUserEmail,
		"date":        json.Date,
	})
	if errInsert != nil {
		log.Fatalf("Failed adding: %v", errInsert)
		c.JSON(http.StatusBadRequest, gin.H{"error": errInsert.Error()})
		return nil
	}

	c.JSON(http.StatusOK, gin.H{"message": "successful"})
	return nil
}

func GetDishes(client *firestore.Client, ctx context.Context, getDishesParam DishGetRequestParams) ([]Dish, error) {
	dishes := []Dish{}
	// var iter *firestore.DocumentIterator
	fmt.Println(getDishesParam)

	iter := client.Collection("dishes").Where("authorEmail", "==", getDishesParam.AuthorEmail).Where("date", "==", getDishesParam.Date).Documents(ctx)

	for {
		doc, err := iter.Next()
		if err == iterator.Done {
			break
		}

		// Convert the map to JSON
		jsonData, _ := json.Marshal(doc.Data())

		// Convert the JSON to a struct
		var structData Dish
		json.Unmarshal(jsonData, &structData)

		structData.Id = doc.Ref.ID

		dishes = append(dishes, structData)
	}

	fmt.Println(dishes)

	return dishes, nil
}
