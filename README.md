# Aemeath 🚀

**Aemeath** adalah framework web Go modern yang dirancang untuk memberikan *Developer Experience* (DX) tingkat atas dengan **Type-Safety end-to-end**. 

Dibangun di atas `net/http` (Go 1.22+), Aemeath memangkas boilerplate, menyatukan validasi request, menghadirkan dokumentasi OpenAPI otomatis, dan secara cerdas menghasilkan Client TypeScript & Go. Semua itu dibungkus dalam API yang ergonomis tanpa mengorbankan performa khas Go.

## ✨ Fitur Utama

- **Type-Safe Routing**: Binding request otomatis (Path, Query, Body) ke dalam struct Go.
- **Validasi Terintegrasi**: Cukup gunakan tag `validate:"required"`.
- **OpenAPI & Swagger UI**: Dokumentasi API di-generate otomatis di `/docs`.
- **Eden-style Client Generator**: Menghasilkan kode TypeScript di `/client.ts` dan Client Go tanpa *codegen* manual.
- **Route Group**: Kelompokkan rute dengan prefix dan middleware khusus.
- **Middleware Bawaan**: CORS, Logger, dan Recover (Anti-Panic).
- **Static File Server**: Sajikan file HTML/CSS/JS dengan mudah.

---

## 📦 Instalasi

Pastikan kamu menggunakan **Go 1.22** atau versi terbaru.

```bash
go get github.com/naufalraism/aemeath
```

---

## 🚀 Quick Start

```go
package main

import (
	"log"
	"github.com/naufalraism/aemeath"
)

type HelloIn struct {
	Name string `path:"name"`
	Lang string `query:"lang"`
}
type HelloOut struct {
	Message string `json:"message"`
}

func main() {
	app := aemeath.New()

	// 1. Tambahkan Middleware Global
	app.Use(aemeath.Logger(), aemeath.Recover(), aemeath.CORS())

	// 2. Daftarkan Route
	aemeath.GET(app, "/hello/{name}", func(c *aemeath.Ctx, in HelloIn) (HelloOut, error) {
		greeting := "Halo"
		if in.Lang == "en" {
			greeting = "Hello"
		}
		return HelloOut{Message: greeting + ", " + in.Name}, nil
	}, aemeath.Summary("Menyapa Pengguna"))

	// 3. Nyalakan Server
	log.Println("Server berjalan di http://localhost:3000")
	log.Fatal(app.Listen(":3000"))
}
```

---

## 🗂️ Route Group & Middleware

Kelompokkan rute dan tambahkan "satpam" (middleware) khusus untuk grup tersebut.

```go
// Middleware khusus Admin
authAdmin := func(next aemeath.HandlerFunc) aemeath.HandlerFunc {
	return func(c *aemeath.Ctx) error {
		// Cek token admin di sini
		return next(c)
	}
}

// Buat Group
admin := app.Group("/admin", authAdmin)

// Daftarkan rute ke Group
aemeath.GroupGET(admin, "/dashboard", func(c *aemeath.Ctx, _ struct{}) (map[string]string, error) {
	return map[string]string{"status": "Welcome Admin"}, nil
})
```

---

## 🛡️ Validasi & Pointer (Penting!)

Aemeath memvalidasi input secara otomatis. Untuk field **scalar** (`int`, `bool`, `float`) yang wajib diisi, kamu **wajib** menggunakan pointer (`*int`, `*bool`) agar Aemeath bisa membedakan antara "tidak dikirim" dan "dikirim bernilai 0".

```go
type CreateUserIn struct {
	// String aman tanpa pointer
	Name  string `json:"name" validate:"required"`
	Email string `json:"email" validate:"required"`
	
	// Integer WAJIB pointer jika required
	Age   *int   `json:"age" validate:"required"`
}
```
*Catatan: Jika kamu tidak menggunakan pointer pada integer `required`, Aemeath akan melakukan **panic saat startup** untuk mencegah bug validasi di kemudian hari.*

### Validasi Nested
`validate:"required"` berlaku **rekursif** ke nested struct:
```go
type Address struct {
    City string `json:"city" validate:"required"`
}
type CreateUser struct {
    Name    string  `json:"name" validate:"required"`
    Address Address `json:"address"` // City ikut divalidasi
}
```

### Batasan Body
Ukuran body JSON dibatasi `aemeath.DefaultMaxBodyBytes` (default **1 MiB**).
```go
aemeath.DefaultMaxBodyBytes = 10 << 20 // 10 MiB
```
Body wajib mengirim header `Content-Type: application/json` bila berisi JSON.

---

## ⚡ Client Generator (TypeScript & Go)

Ini adalah fitur *killer* Aemeath. Kamu tidak perlu menulis ulang tipe data di frontend.

### 1. TypeScript Client
Aktifkan generator dengan `app.EnableClient()`. Kemudian akses `http://localhost:3000/client.ts`:
```ts
import { createClient } from "./client";
const api = createClient({ baseUrl: "http://localhost:3000" });

// Full Type-Safe & Autocomplete!
const res = await api.hello({ path: { name: "Budi" }, query: { lang: "id" } });
```

**Aturan Requiredness TS:**
- **Input** (path, query, body): field wajib **hanya jika** `validate:"required"`.
- **Output**: field wajib **kecuali** bertag `json:",omitempty"`.
- Jika satu struct dipakai sebagai input **dan** output, generator menghasilkan dua interface terpisah — `FooIn` dan `FooOut`.

### 2. Go Client (Tanpa Codegen)
Definisikan kontrak sekali, gunakan di server dan client.

```go
// Definisikan kontrak
var Hello = aemeath.NewEndpoint[HelloIn, HelloOut]("GET", "/hello/{name}")

// Di Server:
aemeath.Register(app, Hello, handler)

// Di Client (dengan timeout default 30 detik):
client := aemeath.NewClient("http://localhost:3000")
out, err := Hello.Call(context.Background(), client, HelloIn{Name: "Budi"})
```
Error >= 400 dikembalikan sebagai `*aemeath.HTTPError`.

---

## 📖 Dokumentasi Otomatis (OpenAPI)

Aktifkan dokumentasi dengan:
```go
app.EnableDocs(aemeath.Info{
	Title:   "Aemeath API",
	Version: "1.0.0",
})
```
Buka `http://localhost:3000/docs` untuk melihat antarmuka Scalar API Reference.

---

## 📂 Static File Server

Menyajikan file statis (HTML/CSS/Gambar).
```go
// Menyajikan folder "./public" di URL "/public"
app.Static("/public", "./public")
```

---

## 🧪 Menjalankan Test

Aemeath dilengkapi dengan unit test.
```bash
go test ./...
```

## 📝 Catatan Teknis
- **Route path** menggunakan sintaks Go 1.22: `/hello/{name}`, bukan `/hello/:name`.
- **Body di `DELETE`** belum didukung (hanya `POST`, `PUT`, `PATCH`).
- **OpenAPI schema** masih inline (belum `$ref`).

## 📝 Lisensi
MIT License.