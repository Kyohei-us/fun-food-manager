package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"os"
	"strconv"
	"time"

	_ "github.com/lib/pq"

	firebase "firebase.google.com/go"
	"firebase.google.com/go/auth"
	"github.com/gin-gonic/contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
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

type DishRecordGetRequestParams struct {
	Uid  string `json:"uid"`
	Date string `json:"date"`
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

	// user=ffmanager password=ffmanager dbname=ffmanagerdb sslmode=disable
	db, err := sql.Open("postgres", os.Getenv("DATABASE_URL"))

	if err != nil {
		fmt.Println(err)
	}

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
			UserSignup(c, authClient, db, ctx)
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

		api.GET("/dishrecord", func(c *gin.Context) {
			var json DishRecordGetRequestParams
			ownedbyloggedinuser := c.Query("ownedbyloggedinuser")
			if ownedbyloggedinuser == "" {
				ownedbyloggedinuser = "false"
			}
			ownedbyloggedinuserbool, _ := strconv.ParseBool(ownedbyloggedinuser)
			if ownedbyloggedinuserbool {
				json.Uid, _ = FindUserUidFromSession(c, authClient, ctx)
			} else {
				json.Uid = ""
			}
			json.Date = c.Query("date")

			dishes, err := GetDishRecords(authClient, db, ctx, json)
			fmt.Println(dishes)
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

		api.POST("/dishrecord", func(c *gin.Context) {
			CreateDishRecord(c, authClient, db, ctx)
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

	defer db.Close()
}

func CreateFirebaseAuthClient(ctx context.Context, app *firebase.App) (*auth.Client, error) {
	client, err := app.Auth(ctx)
	return client, err
}

func UserSignup(c *gin.Context, authClient *auth.Client, db *sql.DB, ctx context.Context) {
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

	c.JSON(http.StatusOK, gin.H{
		"message": "successful",
	})
}

func CreateUser(db *sql.DB, ctx context.Context, uid string) error {
	fmt.Printf("Create user with uid: %s\n", uid)

	var uidReturn string
	id := 3
	err := db.QueryRow("INSERT INTO users(uid, uemail) VALUES($1, $2) RETURNING uid", id).Scan(&uidReturn)

	if err != nil {
		fmt.Println(err)
	}

	fmt.Println(uidReturn)

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

func CreateDishRecord(c *gin.Context, authClient *auth.Client, db *sql.DB, ctx context.Context) error {
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

	tx, errTx := db.Begin()

	if errTx != nil {
		c.JSON(http.StatusOK, gin.H{"message": errTx.Error()})
		return errTx
	}

	var categoryId int
	queryRowRes := tx.QueryRow(`
		INSERT INTO dishcategory (categoryname)
		SELECT CAST($1 as varchar(128))
		WHERE NOT EXISTS (SELECT * FROM dishcategory where categoryname = CAST($1 as varchar(128)))
		RETURNING categoryid`, json.Category).Scan(&categoryId)

	switch {
	case queryRowRes == sql.ErrNoRows:
		// TODO:
		fmt.Printf("1対象のレコードは存在しません。: %v", queryRowRes)
		c.JSON(http.StatusOK, gin.H{"message": queryRowRes.Error()})
		return queryRowRes
	case queryRowRes != nil:
		fmt.Printf("1値の取得に失敗しました。: %v", queryRowRes)
		c.JSON(http.StatusOK, gin.H{"message": queryRowRes.Error()})
		return queryRowRes
	default:
		fmt.Printf("登録ID=%d\n", categoryId)
	}

	fmt.Println(categoryId)

	var dishid int
	errIns2 := tx.QueryRow(`
		INSERT INTO dish (dishname, categoryid, dishpoints) 
		SELECT CAST($1 as varchar(128)), $2, $3
		WHERE NOT EXISTS (SELECT * FROM dish where dishname = CAST($1 as varchar(128)))
		RETURNING dishid`, json.Name, categoryId, json.Points).Scan(&dishid)

	switch {
	case errIns2 == sql.ErrNoRows:
		// TODO:
		fmt.Printf("2対象のレコードは存在しません。: %v", errIns2)
		c.JSON(http.StatusOK, gin.H{"message": errIns2.Error()})
		return errIns2
	case errIns2 != nil:
		fmt.Printf("2値の取得に失敗しました。: %v", errIns2)
		c.JSON(http.StatusOK, gin.H{"message": errIns2.Error()})
		return errIns2
	default:
		fmt.Printf("登録ID=%d\n", categoryId)
	}

	_, errIns3 := tx.Exec(`
		INSERT INTO dishrecord (dishid, uid, registerdate) 
		SELECT $1, $2, $3
		WHERE NOT EXISTS (SELECT * FROM dishrecord where dishid = $1 AND uid = CAST($2 as varchar(128)) AND registerdate = CAST($3 as varchar(128)))
	`, dishid, userUid, json.Date)

	if errIns3 != nil {
		fmt.Println(3, errIns3.Error())
		c.JSON(http.StatusOK, gin.H{"message": errIns3.Error()})
		return errIns3
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	c.JSON(http.StatusOK, gin.H{"message": "successful"})
	return nil
}

func GetDishRecords(authClient *auth.Client, db *sql.DB, ctx context.Context, getDishesParam DishRecordGetRequestParams) ([]Dish, error) {
	// var iter *firestore.DocumentIterator
	fmt.Println(getDishesParam)

	var queryStatement = `SELECT dish.dishname, dishcategory.categoryname, dish.dishpoints, dishrecord.registerdate, dishrecord.uid
		FROM dishrecord 
		INNER JOIN dish 
		ON dishrecord.dishid = dish.dishid
		INNER JOIN dishcategory 
		ON dish.categoryid = dishcategory.categoryid`
	var rows *sql.Rows
	var err error
	if getDishesParam.Uid != "" && getDishesParam.Date != "" {
		queryStatement = queryStatement + " WHERE uid = $1 AND registerdate = $2"
		rows, err = db.Query(queryStatement, getDishesParam.Uid, getDishesParam.Date)
	} else if getDishesParam.Uid != "" {
		queryStatement = queryStatement + " WHERE dishrecord.uid = $1"
		rows, err = db.Query(queryStatement, getDishesParam.Uid)
	} else if getDishesParam.Date != "" {
		queryStatement = queryStatement + " WHERE registerdate = $1"
		rows, err = db.Query(queryStatement, getDishesParam.Date)
	} else {
		rows, err = db.Query(queryStatement)
	}

	if err != nil {
		fmt.Println(err)
	}

	var es []Dish
	for rows.Next() {
		var e Dish
		var uid string
		rows.Scan(&e.Name, &e.Category, &e.Points, &e.Date, &uid)
		fmt.Println(e.Date)
		parsedDate, _ := time.Parse("2006-01-02", e.Date)
		fmt.Println(e.Date, parsedDate)
		e.Date = parsedDate.Format("2006-01-02")
		user, _ := authClient.GetUser(ctx, uid)
		fmt.Println(user)
		e.AuthorEmail = user.Email
		es = append(es, e)
	}

	return es, nil
}
