# Muzak documentation (0.2.7)

Muzak is a type-safe web framework for Go, built on net/http and Go 1.27 with
no third-party dependencies. A handler is an ordinary typed function: its input
type is the request and its return type is the response body, both checked at
compile time. Install it with `go get muzak.dev/framework`.

This describes Muzak 0.2.7, the current release. Older versions are indexed under https://muzak.dev/docs/<version>/llms.txt.

The page index is at https://muzak.dev/llms.txt.
Every page below is also served as HTML under https://muzak.dev/docs/0.2.7.


--------------------------------------------------------------------------------
title: "Introduction"
description: "Muzak is a type-safe web framework for Go. You write plain typed functions, and the framework turns them into an HTTP API with validation, dependency injection and OpenAPI docs."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/getting-started/introduction"
--------------------------------------------------------------------------------

# Introduction

Muzak is a web framework for Go. You use it to build HTTP APIs: a function becomes a
route, the input type of that function is the request, and the return type is the
response body. Both are checked when you compile, not when a request arrives.

The framework is built on `net/http` and Go 1.27, with no third-party dependencies at
all. The pieces you reach for every day (routing, request binding, validation,
dependency injection, configuration, logging, OpenAPI) are part of the framework, not
separate modules you have to wire together yourself.

Here is a complete application. It answers `GET /users/{username}` with a JSON object.

```go
package main

import (
    "log"

    "muzak.dev/framework"
)

type Params struct {
    Username string `path:"username" doc:"The username to look up"`
}

type UserOut struct {
    Username string `json:"username"`
}

func main() {
    app := muzak.New(muzak.AppOptions{Title: "Hello", Version: "1.0.0"})

    app.Get("/users/{username}", func(ctx *muzak.Context, in Params) (UserOut, error) {
        return UserOut{Username: in.Username}, nil
    })

    log.Fatal(app.RunSignals())
}
```

`Params` is the request. `UserOut` is the response. You never write the type arguments
for `app.Get`: Go 1.27 added generic methods and generalized function type inference, so
the router reads both types off the handler literal.

## What you get

- **Routing from typed functions.** A handler is `func(ctx *muzak.Context, in In) (Out, error)`.
  There is no wrapper type around the response and no filtering pass at run time, so a
  field you did not declare on `Out` cannot leak.
- **Typed request data.** Struct tags say where each field comes from: `path`, `query`,
  `header`, `cookie`, `form`, `file`, or the JSON body when a field carries none of them.
  Bad input is rejected with `422` before your function runs.
- **Validation against the field itself.** `v.String(&in.Email).Trim().Lower().Required().Email()`
  names the field by address, so renaming it is a change the compiler checks and there is
  no tag string to typo.
- **Dependency injection in two shapes.** A guard validates and produces nothing; a
  provider produces a typed value that the handler reads with `muzak.From[T](ctx)`.
- **Nested routers.** A package exports its own router and stays unaware of the prefix,
  tags and guards it will eventually run under. The application decides where things
  mount.
- **OpenAPI for free.** Routes, models and validation rules produce an OpenAPI 3.1
  document at `/openapi.json`. A documentation page is one import away, and costs a
  service that does not want one nothing at all.
- **API versioning.** A route declares the version or versions it answers, read from the
  path, a header, the `Accept` header or a function of your own.
- **Real-time built in.** `Router.WS` speaks RFC 6455 WebSockets and `Router.SSE` serves
  typed server-sent events, both behind the same middleware, guards and binding as any
  other route.
- **Safe defaults.** Every listener timeout is non-zero, bodies are capped, unknown JSON
  members are rejected, CORS denies everything until a policy is written, and a panic
  becomes a generic 500 with the stack in the log rather than in the response.

## What a bigger application looks like

Each router is written on its own. The application decides where they mount and what
protects them, and that decision lives in one visible place.

```go
settings := muzak.MustLoadConfig[core.Settings](muzak.EnvFile(".env"))

app := muzak.New(muzak.AppOptions{
    Title:   "Awesome API",
    Version: "1.0.0",
    Addr:    settings.Addr,
},
    muzak.WithDependencies(core.GetQueryToken),
    muzak.WithSingleton(settings),
)

app.Include(routers.Users())
app.Include(routers.Items())
app.Include(routers.Admin(),
    muzak.WithPrefix("/admin"),
    muzak.WithTags("admin"),
    muzak.WithDependencies(core.GetTokenHeader(settings)),
)

log.Fatal(app.RunSignals())
```

`App` embeds `*Router`, so `app.Get(...)` works at the root using the same generic
methods any nested router uses.

## Where to go next

Start with [First Steps](/docs/getting-started/first-steps) to create a project
and run it. After that, [Routers](/docs/getting-started/routers) covers paths
and methods, [Request Data](/docs/getting-started/request-data) covers how the
input struct is filled in, and [Dependencies](/docs/getting-started/dependencies)
shows how services and resources reach your handlers.


--------------------------------------------------------------------------------
title: "First Steps"
description: "Create a Muzak project, write your first route, run the server and open the generated documentation."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/getting-started/first-steps"
--------------------------------------------------------------------------------

# First Steps

Muzak needs Go 1.27. The framework uses generic methods, generalized function type
inference, `encoding/json/v2` and the standard library `uuid` package, none of which
exist in earlier versions.

```bash
go version
```

## Install

Create a module and add the dependency.

```bash
mkdir awesome-api && cd awesome-api
go mod init awesome-api
go get muzak.dev/framework
```

## The smallest application

```go [cmd/main.go]
package main

import (
    "log"

    "muzak.dev/framework"
)

type HelloOut struct {
    Message string `json:"message"`
}

func main() {
    app := muzak.New(muzak.AppOptions{
        Title:   "Awesome API",
        Version: "1.0.0",
        Addr:    ":8080",
    })

    app.Get("/", func(ctx *muzak.Context, _ muzak.Empty) (HelloOut, error) {
        return HelloOut{Message: "Hello from Muzak"}, nil
    })

    log.Fatal(app.RunSignals())
}
```

Run it:

```bash
go run ./cmd
```

```
14:32:07.482 INFO  [Server]        Starting Muzak application...
14:32:07.492 INFO  [Server]        Listening on :8080
```

And call it:

```bash
curl http://localhost:8080/
```

```json
{"message":"Hello from Muzak"}
```

### What each piece does

- `muzak.New` builds the application. `AppOptions` embeds `OpenAPIOptions` and
  `ServerOptions`, which is why `Title` and `Addr` sit next to each other in one literal.
- `app.Get` registers a route. `App` embeds `*Router`, so the root application has the
  same registration methods any nested router has.
- `muzak.Empty` is the input type for a route that reads nothing from the request.
  Binding is skipped entirely for it.
- `HelloOut` is the response model. What the handler returns is what the client receives.
- `app.RunSignals` builds the application, starts the lifecycle components, listens, and
  shuts down gracefully on `SIGINT` or `SIGTERM`.

## The generated documentation

Where it is, is the last thing the server says as it starts:

```
INFO [Server]  Listening on [::]:8080  scheme=http
INFO [Docs]    OpenAPI document at http://localhost:8080/openapi.json  ui="set AppOptions.DocsUI to serve a documentation page"
```

Every application publishes that document. It is generated from the routes and the
types, and it is all a client generator, a linter or another service needs:

```bash
curl http://localhost:8080/openapi.json
```

### Adding the dashboard

Rendering the document is a separate module, so a service that wants no page carries
none. Add the import and the option, and `/docs` starts serving one:

```bash
go get muzak.dev/openapi
```

```go [main.go]
import (
    "muzak.dev/framework"
    "muzak.dev/openapi/ui"
)

app := muzak.New(muzak.AppOptions{
    Title:  "Awesome API",
    DocsUI: ui.Files(),
})
```

Restart, and the log names the page instead:

```
INFO [Docs]    Documentation at http://localhost:8080/docs  openapi=http://localhost:8080/openapi.json
```

Open it. The page is served from the binary itself: it fetches nothing from a third
party and runs under a content security policy that hashes its own inline script.
Operations are grouped by tag, every schema is an outline you can expand, and each
operation has a console that sends the request and shows you what came back -- or hands
you the same request as a `curl` command.

Nothing about this happens at run time. The dashboard is embedded in the binary that
imports it, so an air-gapped deployment gets a working page with no further setup, and
one that never imports it downloads none of those bytes.

### Where they are served

Both paths are configurable, and both can be turned off entirely:

```go
app := muzak.New(muzak.AppOptions{
    Title:       "Awesome API",
    DocsUI:      ui.Files(),
    DocsPath:    "/reference",     // defaults to "/docs"
    OpenAPIPath: "/spec.json",     // defaults to "/openapi.json"
})
```

```go
app := muzak.New(muzak.AppOptions{
    Title:       "Awesome API",
    DisableDocs: true,             // serve neither the page nor the document
})
```

Leaving `DocsUI` unset serves no page while keeping the document; `DisableDocs` stops
both. With no page configured, `DocsPath` is not reserved either, so a route of your own
may answer `/docs`.

## Reading the request

A handler asks for what it needs by declaring an input type. Each field says where it
comes from.

```go [cmd/main.go]
type SearchIn struct {
    Query string `query:"q" doc:"What to search for"`
    Limit int    `query:"limit" default:"20" doc:"How many results to return"`
    Page  *int   `query:"page" doc:"Which page to return, if any"`
}

type SearchOut struct {
    Query   string   `json:"query"`
    Limit   int      `json:"limit"`
    Results []string `json:"results"`
}

func main() {
    app := muzak.New(muzak.AppOptions{Title: "Awesome API"})

    app.Get("/search", func(ctx *muzak.Context, in SearchIn) (SearchOut, error) {
        return SearchOut{Query: in.Query, Limit: in.Limit, Results: nil}, nil
    })

    log.Fatal(app.RunSignals())
}
```

```bash
curl 'http://localhost:8080/search?q=muzak&limit=nope'
```

```json
{
  "error": {
    "code": "validation_error",
    "message": "The request could not be validated.",
    "status": 422,
    "details": [
      { "field": "limit", "location": "query", "issue": "must be a valid integer" }
    ]
  },
  "request_id": "0611f4b2-2f0a-4b57-9c1a-6e6a2e2f9b31"
}
```

Every offending field is reported at once, and the `request_id` in the body is the same
identifier the server wrote to its log and returned in the `X-Request-Id` header.

## Project layout

One file is fine for a demonstration. A real service outgrows it quickly, so the layout
below is the one the framework's own example uses. `cmd/main.go` does nothing but
compose, and every piece of behaviour lives in a package of its own.

```
awesome-api/
├── cmd/
│   └── main.go        composition, and nothing else
├── core/              configuration, guards, dependencies, managed resources
├── schemas/           the request and response models the API exposes
├── handlers/          the functions that answer requests
├── routers/           which handler answers which path
├── .env
└── go.mod
```

The dependencies point one way. Routers know handlers, handlers know schemas and core,
and core knows nothing about any of them, so every package is testable on its own.

Here is the same hello world, written that way.

::code-group

```go [cmd/main.go]
package main

import (
    "log"

    "awesome-api/routers"
    "muzak.dev/framework"
)

func main() {
    app := muzak.New(muzak.AppOptions{
        Title:   "Awesome API",
        Version: "1.0.0",
        Addr:    ":8080",
    })

    app.Include(routers.Users())

    log.Fatal(app.RunSignals())
}
```

```go [routers/users.go]
package routers

import (
    "awesome-api/handlers"
    "muzak.dev/framework"
)

// Users returns the users router. It knows nothing about where it will be
// mounted or what will guard it.
func Users() *muzak.Router {
    r := muzak.NewRouter(muzak.WithTags("users"))

    r.Get("/users/{username}", handlers.ReadUser,
        muzak.Summary("Read a user by name"))

    return r
}
```

```go [handlers/users.go]
package handlers

import (
    "awesome-api/schemas"
    "muzak.dev/framework"
)

// ReadUser returns one user by name.
func ReadUser(ctx *muzak.Context, in schemas.UserLookupParams) (schemas.UserOut, error) {
    return schemas.UserOut{Username: in.Username}, nil
}
```

```go [schemas/users.go]
package schemas

import "muzak.dev/framework"

type UserLookupParams struct {
    Username string `path:"username" doc:"The username to look up"`
}

type UserOut struct {
    Username string `json:"username" doc:"The user's login name"`
    Email    string `json:"email,omitzero" doc:"Where we reach the user"`
}

// Validate constrains the username to what the store can hold.
func (in *UserLookupParams) Validate(v *muzak.Validation) {
    v.String(&in.Username).Trim().Lower().Required().MinLen(2).MaxLen(32).
        Matches(`^[a-z0-9_]+$`).
        Message("may only contain lower case letters, digits and underscores")
}
```

::

## Catching mistakes before the socket opens

Route problems are collected while the application is built, not while it is serving. A
duplicate route, an input type that cannot be bound, a path parameter no field reads, a
duplicate operation identifier and a malformed prefix are all reported together.

```go
if err := app.Build(); err != nil {
    log.Fatal(err)
}
```

`Build` is called for you by `Run`, `RunContext`, `RunSignals` and `ServeHTTP`, so
calling it directly is only useful to surface configuration errors early, which is what a
start-up check or a test wants. Building is idempotent: the work happens once, and later
calls return the same result.

## Where to go next

[Routers](/docs/getting-started/routers) covers paths, methods and how routers
compose. [Request Data](/docs/getting-started/request-data) covers every place
an input field can be read from.


--------------------------------------------------------------------------------
title: "Routers"
description: "Register routes with typed handlers, group them into routers, and mount those routers under prefixes, tags and guards decided by the application."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/getting-started/routers"
--------------------------------------------------------------------------------

# Routers

A `Router` groups related routes under a shared prefix, tag set and dependency chain.
Routers are built independently and composed with `Router.Include`: a package exports its
own routes and stays unaware of where they will be mounted.

`App` embeds `*Router`, so an application is a router that also knows how to serve.
Everything on this page works the same on both.

## Registering a route

```go
r := muzak.NewRouter(muzak.WithTags("items"))

r.Get("/items/{item_id}", handlers.ReadItem)
```

The registration methods are `Get`, `Post`, `Put`, `Patch`, `Delete` and `Head`, plus
`Handle` for any other method. Each takes a path template, a handler, and any number of
route options. Each returns the `*Route` it registered.

```go
r.Handle(http.MethodOptions, "/things", handlers.DescribeThings)
```

There is no `Router.Options` method. `App.Options` applies configuration to an
application, and since `App` embeds `*Router` the two would shadow each other, so
OPTIONS is registered with `Handle`. In practice you rarely need to: OPTIONS is answered
automatically with an `Allow` header.

`Router.WS`, `Router.SSE` and `Router.SSEHandle` register real-time routes and are
covered in [WebSockets](/docs/realtime/websockets) and
[Server-Sent Events](/docs/realtime/server-sent-events). `Router.Frontend` and
`Router.Static` serve files and are covered in
[Static Files and Frontends](/docs/techniques/static-files).

## The handler shape

Every handler is `func(ctx *muzak.Context, in In) (Out, error)`.

```go
func ReadItem(ctx *muzak.Context, in schemas.ItemParams) (schemas.ItemOut, error) {
    store := muzak.From[*core.ItemStore](ctx)

    item, err := store.Get(in.ID)
    if err != nil {
        return schemas.ItemOut{}, muzak.NotFound("Item not found")
    }
    return schemas.ItemOut{ID: item.ID, Name: item.Name}, nil
}
```

`In` and `Out` are inferred from the handler literal or from the named function, so you
never write the type arguments at the call site. A route that reads nothing declares
`muzak.Empty` as its input:

```go
r.Get("/healthz", func(ctx *muzak.Context, _ muzak.Empty) (schemas.HealthOut, error) {
    return schemas.HealthOut{Status: "ok"}, nil
})
```

## Path templates

A path may contain `{name}` parameters and one trailing `{name...}` wildcard. Matching
walks a segment-wise radix trie and allocates nothing.

```go
r.Get("/users/{username}", handlers.ReadUser)          // one segment
r.Get("/files/{path...}", handlers.ServeFile)          // the rest of the path
```

Every parameter in the template must be read by a field of the input type. A template
parameter nothing binds is reported when the application is built, because it is almost
always a typo:

```go
type ItemParams struct {
    ID string `path:"item_id" doc:"The item to operate on"`
}

r.Get("/items/{item_id}", handlers.ReadItem)
```

Captured values are percent-decoded before the handler runs, so `%2F` reaches you as `/`
rather than as an escape you have to remember to decode.

Paths are matched exactly. `/items/` and `/items` are two different templates, and
nothing redirects between them, so register the one you mean.

Registering two routes for the same method and path is an error, with one exception: an
application with versioning enabled may register several at one path as long as the
versions they answer never overlap. See
[Versioning](/docs/fundamentals/versioning).

## Route options

Options passed after the handler configure that route alone.

```go
r.Post("/items/", handlers.CreateItem,
    muzak.Summary("Create an item"),
    muzak.Description("Creates an item under the identifier the caller chooses."),
    muzak.Status(http.StatusCreated),
    muzak.OperationID("createItem"),
    muzak.WithResponseDoc(http.StatusConflict, "An item with that identifier already exists"))
```

| Option | What it does |
|---|---|
| `Summary(s)` | The one-line description shown beside the operation |
| `Description(s)` | The long-form description, rendered as CommonMark |
| `OperationID(id)` | The operation's identifier, which client generators use to name methods |
| `Status(code)` | The status written when the handler returns without an error |
| `Deprecated()` | Marks the operation deprecated in the document. Changes nothing at runtime |
| `Hidden()` | Leaves the route fully routable but out of the document and the docs page |
| `SkipValidation()` | Stops the input model's `Validate` method from running |
| `AllowUnknownFields()` | Accepts JSON members with no matching field instead of rejecting them |
| `MaxBodySize(n)` | Overrides the request body limit for this route |
| `MaxUploadSize(n)` | Overrides the form body limit for a route that binds `form` or `file` fields |
| `MaxFileSize(n)` | Bounds any single uploaded file |
| `WithResponseDoc(code, desc)` | Documents an outcome the handler produces through an error |
| `WithResponseModel[T](code, desc)` | Documents an outcome and the model its body carries |
| `WithTags(...)` | Adds OpenAPI tags |
| `WithDependencies(...)` | Attaches guards |
| `Needs(provider)` | Declares a request-scoped value dependency |
| `Singleton(provider)` / `WithSingleton(v)` | Publishes a value shared by every request |
| `RateLimit(...)` / `SkipRateLimit()` / `WithRateLimit(...)` | Narrow the rate limit policy |
| `WithWebSocket(...)` / `WithSSE(...)` | Configure a real-time route's connection or stream |
| `WithVersion(...)` | Declare the API version or versions the route answers |

Most of these are `SharedOption`, meaning they are equally valid on a router, where they
apply to every route beneath it. `Summary`, `Description`, `OperationID`, `Status` and
`SkipValidation` belong to a single route; `WithPrefix` belongs to a router.

## Grouping routes into a router

A router is created with the options every route beneath it should inherit.

```go [routers/items.go]
package routers

import (
    "net/http"

    "awesome-api/core"
    "awesome-api/handlers"
    "muzak.dev/framework"
)

// Items returns the items router.
func Items() *muzak.Router {
    r := muzak.NewRouter(muzak.WithTags("items"))

    r.Get("/items/", handlers.ListItems,
        muzak.Summary("List items"))

    r.Get("/items/{item_id}", handlers.ReadItem,
        muzak.Summary("Read an item"),
        muzak.WithResponseDoc(http.StatusNotFound, "The item does not exist"),
        // Only this route resolves the caller, so only this route pays for it.
        muzak.Needs(core.GetCurrentUser))

    r.Post("/items/", handlers.CreateItem,
        muzak.Summary("Create an item"),
        muzak.Status(http.StatusCreated),
        muzak.WithResponseDoc(http.StatusConflict, "An item with that identifier already exists"))

    r.Put("/items/{item_id}", handlers.RenameItem,
        muzak.Summary("Rename an item"),
        muzak.WithResponseDoc(http.StatusNotFound, "The item does not exist"))

    return r
}
```

Notice what the router does not decide: it has no prefix, no authentication and no
knowledge of the application it belongs to. Those are decisions made where it is
included.

## Including a router

```go [cmd/main.go]
app := muzak.New(muzak.AppOptions{
    Title:   "Awesome API",
    Version: "1.0.0",
    Addr:    settings.Addr,
},
    muzak.WithDependencies(core.GetQueryToken),
    muzak.WithSingleton(settings),
)

app.Include(routers.Users())
app.Include(routers.Items())

// The admin router is written without a prefix or a guard. Both are applied
// here, which keeps that router reusable and puts the security decision
// somewhere a reviewer will find it.
app.Include(routers.Admin(),
    muzak.WithPrefix("/admin"),
    muzak.WithTags("admin"),
    muzak.WithDependencies(core.GetTokenHeader(settings)),
    muzak.WithResponseDoc(http.StatusTeapot, "I'm a teapot"),
)
```

The child keeps its own configuration and inherits everything from the parent, with the
include's options layered in between. Guards contributed by the parent run before those
contributed at the include, which run before the child's own.

A router may be included only once. Including it twice is reported as an error when the
application is built, because a route has a single resolved path.

### Prefixes

`WithPrefix` must begin with `/` and must not end with one. Prefixes nest, so including a
router that is itself included concatenates both.

```go
muzak.WithPrefix("/admin")     // "/" becomes "/admin/", "/reports" becomes "/admin/reports"
```

```go
v1 := muzak.NewRouter()
v1.Include(routers.Users())
v1.Include(routers.Items())

app.Include(v1, muzak.WithPrefix("/v1"))
```

### Options applied after creation

`App.Options` adds router options to an application after `New` has returned, which is
how a dependency discovered later is published.

```go
models := core.NewModelRegistry()
app.Options(muzak.WithSingleton(models))
```

It must be called before the application is built. Calls made afterwards have no effect,
because the routing tree and the dependency chains are resolved once.

## What the router answers on its own

- **HEAD.** A `GET` route answers `HEAD` automatically by running the handler and letting
  `net/http` discard the body. Registering an explicit `Head` route takes precedence.
- **OPTIONS.** A path with no OPTIONS route answers `204` with an `Allow` header listing
  every method it serves, including the automatic `HEAD` and `OPTIONS`.
- **405.** A path that exists but does not serve the method answers `405` with the same
  `Allow` header and the standard error envelope.
- **404.** A path that matches nothing answers `404`, unless a frontend is mounted and
  claims it.

```bash
curl -i -X OPTIONS http://localhost:8080/items/
```

```
HTTP/1.1 204 No Content
Allow: GET, POST, HEAD, OPTIONS
```

## Inspecting registered routes

`Router.Routes` returns the routes registered directly on a router, excluding those of
any included router. Before the application is built the paths are the ones supplied at
registration, without inherited prefixes.

```go
if err := app.Build(); err != nil {
    log.Fatal(err)
}
for _, route := range app.Routes() {
    log.Printf("%s %s (%s)", route.Method, route.Path, route.OperationID)
}
```

A `*Route` carries `Method`, `Path`, `Summary`, `Description`, `OperationID`, `Tags`,
`Deprecated`, `Hidden` and `Status`. Its fields are filled in when the application is
built and are read-only from that point on. The route being executed is available inside
a handler as `ctx.Route()`.

## Errors reported at build time

These are collected while the application is built and reported together, so a first run
in a new environment lists everything wrong at once instead of one thing per attempt:

- two routes registered for the same method and path, unless versioning tells them apart
- an input or output type that cannot be bound or described
- a path parameter no field binds
- a duplicate operation identifier
- a prefix that does not begin with `/`, or that ends with one
- a router included more than once
- a `MaxConnections` or `MaxStreams` set anywhere but on the application
- a static or frontend mount whose directory does not exist
- a version declared while versioning is off, or a versioning policy missing the field its
  type depends on

## Where to go next

[Request Data](/docs/getting-started/request-data) covers how the input type is
filled in from the request, and [Responses](/docs/getting-started/responses)
covers what happens to the value you return.


--------------------------------------------------------------------------------
title: "Request Data"
description: "How the input struct is filled in from the path, query string, headers, cookies, form and JSON body, which types can be bound, how to keep the body as it arrived for a signature check, and what happens when the client sends something else."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/getting-started/request-data"
--------------------------------------------------------------------------------

# Request Data

A handler's input type is the request. Its fields carry struct tags that say where each
value is read from, and the framework compiles that description into a binding plan once,
when the route is registered. The per-request path walks a list of precompiled setters
and never inspects a type.

```go
type ItemParams struct {
    ID string `path:"item_id" doc:"The item to operate on"`
}

r.Get("/items/{item_id}", func(ctx *muzak.Context, in ItemParams) (ItemOut, error) {
    return ItemOut{ID: in.ID}, nil
})
```

A route that reads nothing declares `muzak.Empty`, and binding is skipped entirely for
it.

## Where a field comes from

| Tag | Source | Example |
|---|---|---|
| `path:"name"` | A `{name}` parameter in the route template | ``ID string `path:"item_id"` `` |
| `query:"name"` | The query string | ``Limit int `query:"limit"` `` |
| `header:"Name"` | A request header, matched case-insensitively | ``Host string `header:"Host"` `` |
| `cookie:"name"` | A cookie sent by the client | ``SessionID string `cookie:"session_id"` `` |
| `form:"name"` | A form value in a multipart or urlencoded body | ``Username string `form:"username"` `` |
| `file:"name"` | A file in a multipart body | ``File muzak.File `file:"file"` `` |
| *none of the above* | The JSON request body | ``Name string `json:"name"` `` |

A field with no location tag is a member of the JSON body, and its `json` tag names the
member. That is the rule the whole binder rests on: you never say "this route takes a
body", you declare fields that have nowhere else to come from.

```go
// Every field here comes from the body, because none of them carries a
// location tag.
type ItemCreateIn struct {
    ID    string `json:"id" doc:"The identifier to create the item under"`
    Name  string `json:"name" doc:"The item's display name"`
    Async bool   `json:"async,omitzero" doc:"Queue the work instead of doing it inline"`
}
```

The `doc` tag is the field's description in the generated OpenAPI document. It works on
parameters and on body members alike.

## Types that can be bound

A parameter arrives as text, so the binder converts it. These types work anywhere a
`path`, `query`, `header`, `cookie` or `form` tag appears:

- `string`
- `bool`, parsed with `strconv.ParseBool`, so `true`, `1`, `t`, `false`, `0` and `f` all work
- every signed and unsigned integer width, and `float32` / `float64`
- `time.Duration`, written the way Go writes it: `1500ms`, `2s`, `1h30m`
- any type implementing `encoding.TextUnmarshaler`, which covers `time.Time` (RFC 3339)
  and `uuid.UUID`
- a slice of any of the above, filled from a repeated parameter
- a pointer to any of the above, which is what makes a field optional

Anything else is a build-time error naming the field, rather than a request that fails
mysteriously later.

```go
type FilterIn struct {
    Since   time.Time     `query:"since" doc:"Only entries after this moment, as RFC 3339"`
    Within  time.Duration `query:"within" default:"24h" doc:"How far back to look"`
    Tags    []string      `query:"tag" doc:"May be repeated"`
    OwnerID uuid.UUID     `query:"owner_id" doc:"Restrict to one owner"`
}
```

```bash
curl 'http://localhost:8080/entries?tag=go&tag=http&within=6h'
```

A repeated parameter fills a slice in the order the values arrived. A non-slice field
takes the first value.

### Types the binder does not know

Implement `encoding.TextUnmarshaler` and the binder accepts your type. The error your
method returns is the issue the client sees, so phrase it to read after the field name.

```go [schemas/feed.go]
// HTTPDate is a time carried in the format HTTP dates use.
//
// It exists because If-Modified-Since is not RFC 3339, which is what a bare
// time.Time parses.
type HTTPDate struct {
    time.Time
}

// UnmarshalText parses an HTTP date, as RFC 9110 defines it.
func (d *HTTPDate) UnmarshalText(text []byte) error {
    parsed, err := http.ParseTime(string(text))
    if err != nil {
        return errors.New("must be an HTTP date, such as Wed, 21 Oct 2026 07:28:00 GMT")
    }
    d.Time = parsed
    return nil
}
```

```go
type ClientHeaders struct {
    IfModifiedSince HTTPDate `header:"If-Modified-Since" doc:"Answer 304 when nothing changed since this time"`
}
```

## Required, optional and defaults

The rule differs by location, because the locations differ in what absence means.

| Location | Required by default | How to change it |
|---|---|---|
| `path` | Always. The route matched, so the value exists | Not applicable |
| `query`, `header`, `cookie` | No | `required:"true"` |
| `form` | Yes, unless the field carries a `default` | `required:"false"` |
| `file` | Yes | `required:"false"` |
| JSON body | The body itself is required whenever any field binds it | Enforce individual members with validation rules |

```go
type UserListQuery struct {
    // Optional, defaulting to 20 when the client sends nothing.
    Limit int `query:"limit" default:"20" doc:"Maximum number of users to return"`
    // Optional, and empty when absent.
    Cursor string `query:"cursor" doc:"Opaque cursor from a previous page"`
}
```

```go
type SessionCookies struct {
    // Required: the request is refused with 422 when the cookie is missing.
    SessionID string `cookie:"session_id" required:"true" doc:"The reader's session"`
}
```

A field that is both `required:"true"` and given a `default` is a build error, because the
two contradict each other.

### Absent against zero

A pointer field stays `nil` when the client sent nothing, which is how an absent value is
told from a zero one.

```go
type WSItemIn struct {
    ItemID string `path:"item_id" doc:"The item being talked about"`
    // Stays nil when the client did not send one.
    Q *int `query:"q" doc:"An optional number echoed back with each reply"`
}
```

```go
if in.Q != nil {
    fmt.Fprintf(w, "q is %d", *in.Q)
}
```

`Context.LookupQuery` and `Context.LookupPath` answer the same question without a
pointer, distinguishing `?token=` from a missing `token`.

## Sharing groups of fields

Embedding a struct promotes its fields onto the input, so a set of parameters several
routes share is declared once.

```go [schemas/feed.go]
// ClientHeaders groups the request headers every read endpoint cares about.
type ClientHeaders struct {
    Host            string   `header:"Host" doc:"The authority the request was addressed to"`
    SaveData        string   `header:"Save-Data" default:"off" doc:"Set to on by a client asking for a smaller payload"`
    IfModifiedSince HTTPDate `header:"If-Modified-Since" doc:"Answer 304 when nothing changed since this time"`
    Traceparent     string   `header:"traceparent" doc:"W3C trace context of the calling span"`
    Tags            []string `header:"X-Tag" doc:"Restrict the feed to these tags; may be repeated"`
}

// SessionCookies groups the cookies the browser sends back.
type SessionCookies struct {
    SessionID       string `cookie:"session_id" required:"true" doc:"The reader's session"`
    FatebookTracker string `cookie:"fatebook_tracker" doc:"Third party analytics cookie, if the reader accepted one"`
    GoogallTracker  string `cookie:"googall_tracker" doc:"Third party analytics cookie, if the reader accepted one"`
}

// FeedIn is two shared groups plus the one parameter this route owns.
type FeedIn struct {
    ClientHeaders
    SessionCookies

    Limit int `query:"limit" default:"20" doc:"How many entries to return"`
}
```

The handler then reads `in.SaveData`, `in.SessionID` and `in.Limit` as if they were
declared on `FeedIn` itself, and the names, docs and defaults live in exactly one place.

## The JSON body

Bodies are decoded with `encoding/json/v2`, and the defaults are strict rather than
forgiving.

- **Unknown members are rejected.** A client's typo becomes an immediate `422` naming the
  member instead of a value that silently disappears.
- **Duplicate members are rejected.**
- **Invalid UTF-8 is rejected.**
- **A body is required** when any field binds it. An empty body reports
  `{"field": "", "location": "body", "issue": "is required"}`.
- **The media type is checked.** `application/json`, anything ending in `+json`, and a
  missing `Content-Type` are accepted. Anything else is `415`.

```bash
curl -X POST http://localhost:8080/items/ \
     -H 'Content-Type: application/json' \
     -d '{"id":"foo","nmae":"Foo"}'
```

```json
{
  "error": {
    "code": "validation_error",
    "message": "The request could not be validated.",
    "status": 422,
    "details": [
      { "field": "nmae", "location": "body", "issue": "is not a field this endpoint accepts" }
    ]
  },
  "request_id": "0611f4b2-2f0a-4b57-9c1a-6e6a2e2f9b31"
}
```

A member of the wrong type reports `has the wrong type, a string is not accepted here`.

Where forward compatibility with clients that send extra members matters more than
catching typos, relax it for the route or the router:

```go
r.Post("/webhooks/stripe", handlers.StripeWebhook, muzak.AllowUnknownFields())
```

Duplicate members and invalid UTF-8 stay rejected regardless.

### Mixing a body with parameters

An input may take some fields from the request line and others from the body.

```go
// The body can only ever reach Name: binding decodes into a scratch value and
// copies out the body-bound fields alone, so a crafted body cannot write to ID.
type ItemRenameIn struct {
    ID   string `path:"item_id" doc:"The item to rename"`
    Name string `json:"name" doc:"The item's new display name"`
}
```

That protection is not advisory. When an input has any located field, the body is decoded
into a scratch value of the same type and only the body-bound fields are copied out, so a
request carrying `{"id": "somebody-elses-item", "name": "x"}` cannot overwrite the path
parameter.

### Form and file fields exclude a JSON body

An input that binds `form` or `file` fields reads a form body, so it cannot also declare
JSON members. A field with no location tag on such an input is a build error that tells
you to tag it with `form` or move it to the path, query, header or cookie. See
[Forms and HTML](/docs/techniques/forms-and-html) and
[File Uploads](/docs/techniques/file-uploads).

## Body size limits

Request bodies are capped at `muzak.DefaultMaxBodySize`, one mebibyte, and a body over
the limit is refused with `413` while it is being read, so the server never buffers more
than the limit.

```go
app := muzak.New(muzak.AppOptions{
    Title:       "Awesome API",
    MaxBodySize: 4 << 20,          // the application-wide default
})

r.Post("/documents/", handlers.CreateDocument,
    muzak.MaxBodySize(16<<20))     // just this route
```

A negative value removes the limit, which is only appropriate behind a proxy that imposes
its own. Routes that bind `form` or `file` fields use `MaxUploadSize` instead, which
defaults to 32 mebibytes.

## The body as it arrived

Binding gives you the decoded value. A request whose authentication covers its **bytes**
needs the bytes, and by the time a handler runs they are gone: the binder read them to
decode the input, and a route declared with `muzak.Empty` drains them so the connection
can be reused.

`CaptureBody` keeps them.

```go
r.Post("/webhooks/stripe", handlers.Stripe,
    muzak.CaptureBody(),
    muzak.Needs(core.VerifyStripeSignature))
```

```go
func VerifyStripeSignature(ctx *muzak.Context) (Verified, error) {
    body, ok := ctx.RawBody()
    if !ok {
        // The route did not declare CaptureBody. Refuse rather than verify
        // against nothing.
        return Verified{}, muzak.NewHTTPError(500, "this route does not capture its body")
    }

    mac := hmac.New(sha256.New, secret)
    mac.Write(body)
    if !hmac.Equal(mac.Sum(nil), signature) {
        return Verified{}, muzak.Unauthorized("the signature does not match")
    }
    return Verified{}, nil
}
```

This is not a niche case. Stripe, GitHub, Slack and Apple's SKAdNetwork postbacks all sign
the raw body, and **a signature must be checked against the original**. Verifying a
re-encoding verifies a different document: a decoder that reorders members, normalises a
number or drops insignificant whitespace produces bytes the client never signed, so every
signature fails and the reason looks like a bug in the client's signing code.

Four things worth knowing:

- **It runs before the dependencies**, so a guard or a provider sees the bytes and an
  unsigned request is refused before anything decodes it. It runs *after* the rate limit is
  counted, so a client past its budget never causes the server to buffer anything.
- **It composes with binding.** The handler still receives its decoded input; the bytes are
  in addition to it, not instead of it.
- **The size limit is the route's own.** `MaxBodySize` already bounds the body and there is
  no second knob. A body over it is refused with `413` rather than truncated, because a
  truncated body fails its signature check and that reads as an attack rather than as the
  oversized request it is.
- **The second return value matters.** A helper shared between a route that captures and
  one that does not would otherwise verify a signature against an empty body and pass.

It is a shared option, so a router of webhook receivers declares it once:

```go
r := muzak.NewRouter(muzak.CaptureBody(), muzak.WithTags("webhooks"))
```

Do not reach for it application-wide. Buffering costs memory for every request in flight,
and a route with nothing to verify has nothing to gain. The bytes live as long as the
handler does and are released with the pooled `Context`, so anything that outlives the
request has to copy them.

## Reading the request directly

Binding covers the typed case. When you need the raw request, the context has it.

```go
func Handler(ctx *muzak.Context, _ muzak.Empty) (Out, error) {
    ctx.Query("token")                 // first value, or ""
    ctx.LookupQuery("token")           // value and whether it was present at all
    ctx.QueryValues("tag")             // every value, in order
    ctx.Header("X-Request-Id")         // first value, matched case-insensitively
    ctx.Cookie("session_id")           // (*http.Cookie, error)
    ctx.PathValue("item_id")           // captured path parameter, percent-decoded
    ctx.LookupPath("item_id")          // value and whether the template declares it
    ctx.RawBody()                      // the body as it arrived, with CaptureBody
    ctx.Request()                      // the underlying *http.Request
    ctx.ClientIP()                     // the address the request is attributed to
    ctx.RequestID()                    // the identifier assigned to this request
    ctx.Route()                        // the route being executed
    ctx.Logger()                       // the request-annotated logger
    ctx.Context()                      // the request's context.Context
    return Out{}, nil
}
```

A `Context` is pooled and reused across requests. It must not be retained or used after
the handler returns; pass `ctx.Context()` to anything that outlives the handler.

## Where to go next

[Validation](/docs/fundamentals/validation) turns a bound value into a checked
one, and [Responses](/docs/getting-started/responses) covers what happens to
what you return. Each location has a page of its own for the details:
[Headers](/docs/techniques/headers),
[Cookies](/docs/techniques/cookies),
[JSON](/docs/techniques/json),
[Forms and HTML](/docs/techniques/forms-and-html) and
[File Uploads](/docs/techniques/file-uploads).


--------------------------------------------------------------------------------
title: "Responses"
description: "The handler's return type is the response model. How status codes, headers, cookies, HTML and empty responses are decided, and when to write the response yourself."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/getting-started/responses"
--------------------------------------------------------------------------------

# Responses

The second type parameter of a handler is the response body, serialized exactly as
returned.

```go
type ItemOut struct {
    ID    string `json:"id" doc:"The item's identifier"`
    Name  string `json:"name" doc:"The item's display name"`
    Owner string `json:"owner,omitzero" doc:"The user the item belongs to"`
}

r.Get("/items/{item_id}", func(ctx *muzak.Context, in ItemParams) (ItemOut, error) {
    return ItemOut{ID: in.ID, Name: "Foo"}, nil
})
```

There is no wrapper type, no `response_model=` and no filtering pass at run time. The
handler's return type **is** the response model, which means a field you did not declare
cannot leak: it is not part of the type, and the compiler is what tells you, not a bug
report.

That is why response models are kept separate from storage types:

```go [core/store.go]
// Item is the store's own type, deliberately separate from the response
// models. A field added here does not become visible to clients until a
// handler puts it on a response type.
type Item struct {
    ID   string
    Name string
}
```

The body is encoded fully into a buffer before anything is written, so a failure part way
through encoding still produces a clean error response instead of a truncated body.
`Content-Type: application/json; charset=utf-8` and `Content-Length` are set for you.

## Status codes

A status that never changes for a route is part of the route.

```go
r.Post("/items/", handlers.CreateItem, muzak.Status(http.StatusCreated))
```

A status that depends on what happened is set in the handler.

```go
func CreateItem(ctx *muzak.Context, in schemas.ItemCreateIn) (schemas.ItemOut, error) {
    store := muzak.From[*core.ItemStore](ctx)

    if err := store.Create(core.Item{ID: in.ID, Name: in.Name}); err != nil {
        return schemas.ItemOut{}, asHTTPError(err)
    }
    if in.Async {
        ctx.SetStatus(http.StatusAccepted)
    }
    return schemas.ItemOut{ID: in.ID, Name: in.Name}, nil
}
```

The two are orthogonal. You never have to choose between returning a value and setting a
status, and you never declare something dynamic as if it were fixed. When both are used
the imperative call wins.

Details worth knowing:

- Without either, the status is `200`.
- `ctx.Status()` reports what will be written, which is the route's declared default until
  `SetStatus` changes it.
- Codes outside 100 to 599 are clamped to 500.
- Calling `SetStatus` after the body has started writing has no effect and is logged at
  warn level, because the status line has by then been sent.
- `204` and `304` write no body at all, whatever the handler returned.

```go
func Feed(ctx *muzak.Context, in schemas.FeedIn) (schemas.FeedOut, error) {
    if !in.IfModifiedSince.IsZero() && !feedUpdatedAt.After(in.IfModifiedSince.Time) {
        ctx.SetStatus(http.StatusNotModified)
        return schemas.FeedOut{}, nil
    }
    // ...
}
```

## Headers

```go
ctx.SetHeader("Last-Modified", feedUpdatedAt.UTC().Format(http.TimeFormat))
ctx.SetHeader("Vary", "Save-Data, X-Tag, Cookie")
ctx.AddHeader("Vary", "Accept-Encoding")   // appends rather than replacing
```

`SetHeader` replaces any value already set; `AddHeader` appends, which is what repeated
headers such as `Set-Cookie` and `Vary` require. Headers must be set before the handler
returns: once the response has begun, `net/http` ignores further changes.

## Cookies

```go
session := uuid.NewV4().String()
ctx.SetCookie(&http.Cookie{
    Name:  "session_id",
    Value: session,
    Path:  "/",
    // HttpOnly keeps the session out of reach of scripts, and SameSite keeps
    // it off cross-site requests. Secure belongs here too once this is served
    // over TLS, which is why it is named rather than omitted.
    HttpOnly: true,
    SameSite: http.SameSiteLaxMode,
    Secure:   false,
    MaxAge:   3600,
})
```

Muzak does not modify the cookie. Setting `Secure`, `HttpOnly` and `SameSite`
appropriately is the caller's job, which is covered in
[Cookies](/docs/techniques/cookies).

## Responses with no body

`muzak.Empty` is also usable as an output type. The generated document then describes no
response content at all, which is the honest description of a route that answers with
nothing.

```go
r.Delete("/items/{item_id}", func(ctx *muzak.Context, in ItemParams) (muzak.Empty, error) {
    store := muzak.From[*core.ItemStore](ctx)
    if _, err := store.Get(in.ID); err != nil {
        return muzak.Empty{}, muzak.NotFound("Item not found")
    }
    return muzak.Empty{}, nil
}, muzak.Status(http.StatusNoContent))
```

Pair it with `muzak.Status(http.StatusNoContent)`. At `200` an `Empty` value still
encodes as `{}`, since that is what it serializes to; at `204` nothing is written.

## HTML

A handler returning `muzak.HTML` bypasses JSON encoding entirely. The string is written
verbatim under `text/html`, and the generated document describes the response as
`text/html` rather than as a JSON schema.

```go
func LoginForm(ctx *muzak.Context, _ muzak.Empty) (muzak.HTML, error) {
    return muzak.HTML(`<body>
<form action="/login/" method="post">
<label>Username <input name="username" autocomplete="username"></label>
<label>Password <input name="password" type="password" autocomplete="current-password"></label>
<input type="submit" value="Sign in">
</form>
</body>`), nil
}
```

Everything else about the route is unchanged, so its status, headers and errors work
exactly as they do for a JSON route. The value is written as given: Muzak does not escape
it, because it cannot tell markup the handler meant from text it did not. Interpolating
anything a client supplied is the handler's job to escape, with `html/template` or
`html.EscapeString`.

## Documenting other outcomes

A handler that reports a failure through an error does not describe that outcome in its
return type, so declare it at registration.

```go
r.Get("/items/{item_id}", handlers.ReadItem,
    muzak.WithResponseDoc(http.StatusNotFound, "The item does not exist"))
```

`WithResponseDoc` is recorded in the OpenAPI document and has no effect at runtime.
Applied to a router, it documents the response for every route beneath it, which is how
`app.Include(admin, muzak.WithResponseDoc(418, "I'm a teapot"))` reads. The body is
described as Muzak's error envelope, because that is what a returned error produces.

When the outcome carries a body of its own instead, name the model:

```go
r.Post("/users/", handlers.CreateUser,
    muzak.Status(http.StatusCreated),
    muzak.WithResponseModel[schemas.ErrorOut](http.StatusBadRequest, "The request was malformed"))
```

One operation can carry a different schema at every status code that way.
[Response Models](/docs/techniques/response-models) covers it in full, including
inheritance, `Empty` and `HTML` models, and what reaches the document.

## Writing the response yourself

For a file download or any other body Muzak should not encode, take the writer.

```go
func Download(ctx *muzak.Context, in FileParams) (muzak.Empty, error) {
    file, err := os.Open(filepath.Join(storageDir, in.Name))
    if err != nil {
        return muzak.Empty{}, muzak.NotFound("no such file")
    }
    defer file.Close()

    stat, err := file.Stat()
    if err != nil {
        return muzak.Empty{}, err
    }

    ctx.SetHeader("Content-Disposition", `attachment; filename="report.pdf"`)
    http.ServeContent(ctx.ResponseWriter(), ctx.Request(), stat.Name(), stat.ModTime(), file)
    return muzak.Empty{}, nil
}
```

Writing to `ctx.ResponseWriter()` bypasses Muzak's response encoding: the returned value
is not serialized and `SetStatus` stops having an effect, so return the zero `Out` value
with a nil error afterwards. The writer supports `http.ResponseController`, so flushing
and hijacking work as usual.

For streaming a sequence of events rather than one body, reach for
[Server-Sent Events](/docs/realtime/server-sent-events) instead, which keeps the
typed contract and the generated documentation.

## Where to go next

[Dependencies](/docs/getting-started/dependencies) covers how services and
resolved values reach a handler,
[Error Handling](/docs/getting-started/error-handling) covers what happens when
a handler returns an error, and [JSON](/docs/techniques/json) covers the encoding
in detail.


--------------------------------------------------------------------------------
title: "Dependencies"
description: "Guards that validate a request, providers that hand a typed value to a handler, and singletons that publish a resource to the whole application."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/getting-started/dependencies"
--------------------------------------------------------------------------------

# Dependencies

Dependencies come in exactly two shapes. A **guard** validates and produces nothing. A
**provider** produces a typed value. Both run before the handler, and both can refuse the
request by returning an error.

## Guards

A guard is `func(ctx *muzak.Context) error`.

```go [core/dependencies.go]
// GetQueryToken is a guard dependency applied to the whole application.
//
// It rejects any request that does not carry a token query parameter. A guard
// produces no value; it either lets the request through or returns the error
// that becomes the response.
func GetQueryToken(ctx *muzak.Context) error {
    if ctx.Query("token") == "" {
        return muzak.BadRequest("token is required")
    }
    return nil
}
```

Attach it with `WithDependencies`, which works on an application, a router, or a single
route.

```go
// Every route in the application sits below this guard.
app := muzak.New(muzak.AppOptions{Title: "Awesome API"},
    muzak.WithDependencies(core.GetQueryToken))

// Only the admin subtree sits below this one.
app.Include(routers.Admin(),
    muzak.WithPrefix("/admin"),
    muzak.WithDependencies(core.GetTokenHeader(settings)))

// Only this route.
r.Post("/reindex", handlers.Reindex,
    muzak.WithDependencies(RequireMaintenanceWindow))
```

Guards run in declaration order, outermost first: those declared on the application run
before those declared when including a router, which run before the route's own. The
first guard to return an error stops the chain and produces the response.

Two shared-secret guards ship with the framework, both comparing in constant time:

```go
muzak.WithDependencies(muzak.RequireBearerToken(settings.AdminToken))
muzak.WithDependencies(muzak.RequireHeaderToken("X-Token", "coneofsilence"))
```

They are meant for the shared-secret case, such as an internal service or a webhook
receiver. Anything involving per-user credentials wants a provider that resolves the user
instead.

## Providers

A provider is `func(ctx *muzak.Context) (T, error)`. Declare it with `Needs` and read the
value with `From`.

```go [core/dependencies.go]
// CurrentUser is the authenticated caller.
type CurrentUser struct {
    Username string
}

// GetCurrentUser resolves the caller from the Authorization header.
func GetCurrentUser(ctx *muzak.Context) (CurrentUser, error) {
    token, present := muzak.BearerToken(ctx)
    if !present {
        return CurrentUser{}, muzak.Unauthorized("unauthorized")
    }
    user, err := lookUp(token)
    if err != nil {
        return CurrentUser{}, muzak.Unauthorized("unauthorized")
    }
    return user, nil
}
```

```go
r.Get("/items/{item_id}", func(ctx *muzak.Context, in ItemParams) (ItemOut, error) {
    user := muzak.From[CurrentUser](ctx)
    return ItemOut{ID: in.ID, Owner: user.Username}, nil
}, muzak.Needs(core.GetCurrentUser))
```

`muzak.From[CurrentUser](ctx)` is checked at compile time. There is no `interface{}`, no
type assertion, no service locator and no string key to mistype. The type parameter of
`Needs` is inferred from the provider, so it is never written at the call site either.

Resolved values live on the request's `Context` and are cleared when it returns to the
pool, so two concurrent requests can never see each other's values.

### From against TryFrom

`From` panics when the route never declared the type, because that is a bug in the wiring
rather than a condition to handle. The panic is caught by the recovery middleware and
reported as a 500 with the details logged, but it is a mistake to fix rather than an error
to recover from.

Where the absence of a dependency is a legitimate state, use `TryFrom`.

```go [core/ratelimit.go]
// UserOrIPTracker spends a request from the caller's budget when there is a
// caller, and from the address's otherwise.
//
// The identity is read with TryFrom rather than From, because most routes here
// resolve no user at all and an absent dependency is a legitimate state for
// this tracker rather than a programming error.
func UserOrIPTracker(ctx *muzak.Context) (string, error) {
    if user, ok := muzak.TryFrom[CurrentUser](ctx); ok {
        return "user:" + user.Username, nil
    }
    return muzak.IPTracker(ctx)
}
```

### Declaring a provider for a whole router

`Needs` is a shared option, so a router can declare it once for everything beneath it.

```go
r := muzak.NewRouter(muzak.WithTags("items"), muzak.Needs(core.GetCurrentUser))
```

Declaring the same type more than once along the chain is allowed, and the most recent
declaration wins, which lets a route override a dependency its router declared.

## The order things run in

For every request:

1. The middleware chain: request identifier, panic recovery, access log, security headers,
   then anything installed with `App.Use`.
2. The rate limit count, unless the policy asked for it after the dependencies.
3. Guards, outermost first.
4. Providers, in declaration order.
5. Binding, which fills the input type from the request.
6. Validation, which runs the input model's `Validate` method.
7. The handler.

Guards running before validation is deliberate: an unauthenticated caller gets `401`, not
a map of your schema.

Two consequences are worth knowing:

- **Every guard runs before any provider**, whatever scope each was declared on. A guard
  cannot read a value a provider produced, including a value published with
  `WithSingleton`, because none of them has resolved yet. Give the guard what it needs by
  closing over it, which is what `core.GetTokenHeader(settings)` does above, or move the
  check into a provider of its own.
- **Binding happens after the dependencies**, so a guard and a provider see the raw request
  through the context rather than the typed input.

## Singletons

A singleton is a value shared by every request rather than resolved per request. There
are two ways to publish one.

`WithSingleton` publishes a value that already exists, which is the usual case:

```go [cmd/main.go]
settings := muzak.MustLoadConfig[core.Settings](muzak.EnvFile(".env"))
store := core.NewItemStore()
models := core.NewModelRegistry()

app := muzak.New(muzak.AppOptions{Title: settings.AppName, Addr: settings.Addr},
    muzak.WithSingleton(settings),
    muzak.WithSingleton(models),
    muzak.WithSingleton(store, store.Lifecycle()),
)
```

Handlers read it by type, with no cast:

```go [handlers/items.go]
func ListItems(ctx *muzak.Context, _ muzak.Empty) (schemas.ItemListOut, error) {
    store := muzak.From[*core.ItemStore](ctx)
    settings := muzak.From[core.Settings](ctx)

    stored := store.List()
    items := make([]schemas.ItemOut, 0, len(stored))
    for _, item := range stored {
        items = append(items, schemas.ItemOut{ID: item.ID, Name: item.Name})
    }
    return schemas.ItemListOut{Items: items, Limit: settings.ItemsPerUser}, nil
}
```

`Singleton` is the lazy counterpart, for a value expensive enough that building it at
start-up is not worth it:

```go
muzak.Singleton(func(ctx *muzak.Context) (*template.Template, error) {
    return template.ParseGlob("templates/*.html")
})
```

The provider runs on the first request that needs the value, receiving that request's
`Context`. It must not retain that context, read request-specific state from it, or return
a value that is unsafe for concurrent use, because every later request shares the same
value. An error from the provider is cached too, so a failing singleton fails every
request rather than being retried.

Whichever form you use, the value is shared, so it must be safe for concurrent use.

### Singletons and lifecycle

If a published value implements `muzak.Lifecycle`, or a `LifecycleFunc` option is
supplied alongside it, the value is also registered as a lifecycle component: started
before the server accepts traffic and stopped after the server has drained. That is what
`store.Lifecycle()` is doing above, and it is covered in
[Lifecycle](/docs/getting-started/lifecycle).

## Errors from a dependency

A guard or a provider that returns an error abandons the request, and the error becomes
the response exactly as one returned from a handler would. Returning
`muzak.Unauthorized("")` is the idiomatic way to reject, and there is a constructor like
it for every status worth naming; any other error type becomes an opaque 500 with the real
cause logged. See
[Error Handling](/docs/getting-started/error-handling).

## A worked example

Here is a WebSocket credential resolved before the handshake completes, which is what
lets an unauthenticated peer receive a readable JSON error rather than a socket that
closes a moment after it opened.

::code-group

```go [core/dependencies.go]
// SessionOrToken is the caller of a WebSocket route, resolved from either a
// session cookie or a query parameter.
//
// A browser cannot set headers on a WebSocket handshake, so the two places a
// credential can arrive are a cookie the browser attaches itself and a query
// parameter the page puts in the URL.
type SessionOrToken struct {
    Value      string
    FromCookie bool
}

func GetSessionOrToken(ctx *muzak.Context) (SessionOrToken, error) {
    if cookie, err := ctx.Cookie("session"); err == nil && cookie.Value != "" {
        return SessionOrToken{Value: cookie.Value, FromCookie: true}, nil
    }
    if token := ctx.Query("token"); token != "" {
        return SessionOrToken{Value: token}, nil
    }
    return SessionOrToken{}, muzak.Unauthorized("a session cookie or a token query parameter is required")
}
```

```go [routers/items.go]
r.WS("/items/{item_id}/ws", handlers.ItemSocket,
    muzak.Summary("Talk to an item over a WebSocket"),
    muzak.Needs(core.GetSessionOrToken))
```

```go [handlers/items.go]
func ItemSocket(ctx *muzak.Context, in schemas.WSItemIn, conn *muzak.WSConn) error {
    session := muzak.From[core.SessionOrToken](ctx)
    // ...
}
```

::

## Where to go next

[Lifecycle](/docs/getting-started/lifecycle) covers resources that must be
opened before traffic and closed after it, and
[Error Handling](/docs/getting-started/error-handling) covers the errors a
dependency returns.


--------------------------------------------------------------------------------
title: "Error Handling"
description: "One error envelope for every failure, errors that carry their own status, errors that stay opaque, and how to replace the shape entirely."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/getting-started/error-handling"
--------------------------------------------------------------------------------

# Error Handling

Every failure renders as one envelope, carrying a machine-readable code, a message safe
to disclose, the status, per-field details and the request identifier that ties the
response to the server's log.

```json
{
  "error": {
    "code": "validation_error",
    "message": "The request could not be validated.",
    "status": 422,
    "details": [
      { "field": "name", "location": "body", "issue": "is required" },
      { "field": "limit", "location": "query", "issue": "must be a valid integer" }
    ]
  },
  "request_id": "0611f4b2-2f0a-4b57-9c1a-6e6a2e2f9b31"
}
```

The Go types are `muzak.ErrorResponse`, `muzak.ErrorBody` and `muzak.ErrorDetail`, so a
client written in Go can decode the envelope with the same definitions the server writes
it from.

## Two kinds of error

Muzak divides every error a handler or a dependency returns into two groups.

**An error that describes itself** reaches the client as written. That means an
`*HTTPError`, a `*ValidationError`, or any error implementing `StatusCoder`.

**Everything else** becomes an opaque `500` whose message and code are fixed constants,
with the real cause written to the log and never transmitted. A nil dereference, a
database driver error or a wrapped file path cannot leak internal state through a
response.

```json
{
  "error": {
    "code": "internal_error",
    "message": "The server could not complete the request.",
    "status": 500
  },
  "request_id": "0611f4b2-2f0a-4b57-9c1a-6e6a2e2f9b31"
}
```

Both the code and the message are fixed constants. Nothing derived from the underlying
error appears anywhere in the response.

## Returning an HTTP error

`NewHTTPError` takes the status and the message the client will read.

```go
return schemas.ItemOut{}, muzak.NewHTTPError(http.StatusNotFound, "Item not found")
```

```json
{
  "error": {
    "code": "not_found",
    "message": "Item not found",
    "status": 404
  },
  "request_id": "0611f4b2-2f0a-4b57-9c1a-6e6a2e2f9b31"
}
```

The status decides the classifier, `404` becoming `not_found`, and the message is
transmitted verbatim. `NewHTTPErrorf` builds that message with a format string:

```go
return schemas.ItemOut{}, muzak.NewHTTPErrorf(http.StatusBadRequest,
    "%q is not a supported currency", in.Currency)
```

```json
{
  "error": {
    "code": "bad_request",
    "message": "\"XBT\" is not a supported currency",
    "status": 400
  },
  "request_id": "0611f4b2-2f0a-4b57-9c1a-6e6a2e2f9b31"
}
```

Because the formatted result is sent to the client, keep internal state out of it. Use
`Wrap` for the parts that should stay server-side.

`*HTTPError` composes:

```go
return Out{}, muzak.PaymentRequired("the card was declined").
    WithCode("card_declined").
    WithDetails(muzak.ErrorDetail{
        Field:    "payment_method",
        Location: "body",
        Issue:    "was declined by the issuer",
    }).
    Wrap(err)
```

```json
{
  "error": {
    "code": "card_declined",
    "message": "the card was declined",
    "status": 402,
    "details": [
      { "field": "payment_method", "location": "body", "issue": "was declined by the issuer" }
    ]
  },
  "request_id": "0611f4b2-2f0a-4b57-9c1a-6e6a2e2f9b31"
}
```

What `Wrap` holds appears nowhere in that response. It is written to the log, next to the
same request identifier the client was given.

| Method | What it does |
|---|---|
| `WithCode(code)` | Overrides the classifier, for a status too coarse on its own |
| `WithDetails(...)` | Attaches per-field details, which appear in `details` |
| `Wrap(err)` | Records a cause that is logged and visible to `errors.Is` and `errors.As`, and never sent |
| `WithMessageKey(key, args...)` | Names a translation for the message, keeping the one already written as the fallback |

Returning one from a handler, a guard or a provider short-circuits the request: nothing
further down the chain runs, and the status, code and message become the response.

## Errors in another language

Every sentence above is translated when the application has a locale configured, and none
of the constructors change. `muzak.Forbidden("")` takes the standard sentence for its
status, and that sentence is what gets looked up.

```yaml [core/locales/es.yml]
es:
  muzak:
    http:
      403: "No tienes acceso a este recurso."
```

A message you write yourself is your own words, and is never replaced by a translation of
something else. Name a translation for it explicitly when you want one:

```go
return muzak.Forbidden("only editors may do that").
    WithMessageKey("errors.access.denied")
```

The code is never translated. Clients branch on it, so a locale file that reworded one
would break every client reading it. See
[Internationalization](/docs/fundamentals/internationalization).

## Errors by name

There is a constructor for each outcome worth a name of its own, so returning the right
response is not a matter of remembering the right number:

```go
return schemas.UserOut{}, muzak.NotFound("no user goes by that name")
```

```json
{
  "error": {
    "code": "not_found",
    "message": "no user goes by that name",
    "status": 404
  },
  "request_id": "0611f4b2-2f0a-4b57-9c1a-6e6a2e2f9b31"
}
```

| Constructor | Status | Code |
|---|---|---|
| `BadRequest(message)` | `400` | `bad_request` |
| `Unauthorized(message)` | `401` | `unauthorized` |
| `PaymentRequired(message)` | `402` | `payment_required` |
| `Forbidden(message)` | `403` | `forbidden` |
| `NotFound(message)` | `404` | `not_found` |
| `MethodNotAllowed(message)` | `405` | `method_not_allowed` |
| `NotAcceptable(message)` | `406` | `not_acceptable` |
| `RequestTimeout(message)` | `408` | `request_timeout` |
| `Conflict(message)` | `409` | `conflict` |
| `Gone(message)` | `410` | `gone` |
| `PreconditionFailed(message)` | `412` | `precondition_failed` |
| `PayloadTooLarge(message)` | `413` | `payload_too_large` |
| `UnsupportedMediaType(message)` | `415` | `unsupported_media_type` |
| `UnprocessableEntity(message)` | `422` | `validation_error` |
| `TooManyRequests(message)` | `429` | `too_many_requests` |
| `InternalServerError(message)` | `500` | `internal_error` |
| `NotImplemented(message)` | `501` | `not_implemented` |
| `BadGateway(message)` | `502` | `bad_gateway` |
| `ServiceUnavailable(message)` | `503` | `service_unavailable` |
| `GatewayTimeout(message)` | `504` | `gateway_timeout` |

Every one of them returns an `*HTTPError`, so `Wrap`, `WithCode` and `WithDetails` chain
onto any of them, and a status without a name of its own is still `NewHTTPError`'s to
produce.

### The standard sentence

Passing an empty message uses the standard wording for that status, which is what a
framework with one exception per status gives you when you raise it with no argument:

```go
return schemas.UserOut{}, muzak.Forbidden("")
```

```json
{
  "error": {
    "code": "forbidden",
    "message": "You do not have access to this resource.",
    "status": 403
  },
  "request_id": "0611f4b2-2f0a-4b57-9c1a-6e6a2e2f9b31"
}
```

### Server errors say what happened, not why

Returning a `5xx` by name is a deliberate act, so unlike an unexpected fault its message
reaches the client. The cause belongs in `Wrap`, which does not:

```go
return schemas.PriceOut{}, muzak.BadGateway("the pricing service is not answering").
    Wrap(err) // dial tcp 10.0.0.7:5432: connect: connection refused
```

```json
{
  "error": {
    "code": "bad_gateway",
    "message": "the pricing service is not answering",
    "status": 502
  },
  "request_id": "0611f4b2-2f0a-4b57-9c1a-6e6a2e2f9b31"
}
```

The address, the port and the driver's wording stay in the log.

## Translating domain errors

Keep HTTP out of your store or service layer, and translate at the edge.

::code-group

```go [core/store.go]
var (
    // ErrItemNotFound reports a lookup for an item that does not exist.
    ErrItemNotFound = errors.New("item not found")
    // ErrItemExists reports a create that collides with an existing item.
    ErrItemExists = errors.New("item already exists")
)
```

```go [handlers/items.go]
// asHTTPError translates a store error into the response it deserves.
//
// The translation lives here rather than in the store, which keeps the store
// free of any knowledge about HTTP, and it is exhaustive rather than a
// fall-through: an error the service does not recognise becomes an opaque 500
// with the real cause logged and never sent.
func asHTTPError(err error) error {
    switch {
    case errors.Is(err, core.ErrItemNotFound):
        // Wrap keeps the store's error in the log without sending it.
        return muzak.NotFound("Item not found").Wrap(err)
    case errors.Is(err, core.ErrItemExists):
        return muzak.Conflict("Item already exists").Wrap(err)
    default:
        return err
    }
}
```

::

```go
func ReadItem(ctx *muzak.Context, in schemas.ItemParams) (schemas.ItemOut, error) {
    store := muzak.From[*core.ItemStore](ctx)

    item, err := store.Get(in.ID)
    if err != nil {
        return schemas.ItemOut{}, asHTTPError(err)
    }
    return schemas.ItemOut{ID: item.ID, Name: item.Name}, nil
}
```

## Your own error types

Implement `StatusCoder` and your type becomes a first-class citizen of the error pipeline
without depending on `*HTTPError`.

```go
type QuotaExceeded struct {
    Plan string
}

func (e *QuotaExceeded) Error() string   { return "quota exceeded for plan " + e.Plan }
func (e *QuotaExceeded) HTTPStatus() int { return http.StatusPaymentRequired }
```

```json
{
  "error": {
    "code": "payment_required",
    "message": "quota exceeded for plan free",
    "status": 402
  },
  "request_id": "0611f4b2-2f0a-4b57-9c1a-6e6a2e2f9b31"
}
```

Two things follow from that. The code is derived from the status with `CodeForStatus`, so
reach for `*HTTPError` and `WithCode` when you want a classifier of your own. And the
message is `err.Error()`, transmitted verbatim, so an error type that implements
`StatusCoder` has declared its `Error()` text safe to disclose. Keep file paths, driver
messages and query fragments out of it.

## Error codes

The `code` member is what clients should branch on, rather than on the status or the
message text.

| Constant | Value |
|---|---|
| `CodeBadRequest` | `bad_request` |
| `CodeUnauthorized` | `unauthorized` |
| `CodePaymentRequired` | `payment_required` |
| `CodeForbidden` | `forbidden` |
| `CodeNotFound` | `not_found` |
| `CodeMethodNotAllowed` | `method_not_allowed` |
| `CodeNotAcceptable` | `not_acceptable` |
| `CodeRequestTimeout` | `request_timeout` |
| `CodeConflict` | `conflict` |
| `CodeGone` | `gone` |
| `CodePreconditionFailed` | `precondition_failed` |
| `CodePayloadTooLarge` | `payload_too_large` |
| `CodeUnsupportedMediaType` | `unsupported_media_type` |
| `CodeValidationError` | `validation_error` |
| `CodeTooManyRequests` | `too_many_requests` |
| `CodeNotImplemented` | `not_implemented` |
| `CodeBadGateway` | `bad_gateway` |
| `CodeServiceUnavailable` | `service_unavailable` |
| `CodeGatewayTimeout` | `gateway_timeout` |
| `CodeInternalError` | `internal_error` |
| `CodeClientError` | `client_error` |

`muzak.CodeForStatus(status)` returns the default code for a status: a recognised status
gets its specific classifier, any other 4xx becomes `client_error`, and everything else
becomes `internal_error`.

## Validation failures

A `*ValidationError` renders as `422` classified `validation_error`, with one entry in
`details` per offending field, so a client learns about every mistake at once instead of
one per round trip. Binding failures and rule failures merge into the same envelope, each
carrying the location the binder established.

```json
{
  "error": {
    "code": "validation_error",
    "message": "The request could not be validated.",
    "status": 422,
    "details": [
      { "field": "username", "location": "body",  "issue": "is reserved" },
      { "field": "email",    "location": "body",  "issue": "must be a valid email address" },
      { "field": "password", "location": "body",  "issue": "must be at least 12 characters" },
      { "field": "limit",    "location": "query", "issue": "must be between 1 and 100" }
    ]
  },
  "request_id": "0611f4b2-2f0a-4b57-9c1a-6e6a2e2f9b31"
}
```

`location` is one of `path`, `query`, `header`, `cookie` or `body`. See
[Validation](/docs/fundamentals/validation) for the rules that produce these.

## Failures the framework reports on its own

| Status | When |
|---|---|
| `404` | No route matched the path, and no frontend claimed it |
| `405` | The path exists but not for that method. The response carries `Allow` |
| `413` | The request body exceeded the route's limit, refused while it was being read |
| `415` | A body arrived under a media type the route cannot decode |
| `422` | Binding or validation failed |
| `429` | A rate limit was exceeded. The response carries `Retry-After` and the `RateLimit` headers |
| `503` | The connection or stream budget is full, or a rate limit storage could not answer |
| `500` | A panic, or any error that does not describe itself |

## Panics

A panic escaping a handler or a dependency is caught, logged with its stack trace, the
request identifier, the method and the route, and answered with the generic `500` above.
Nothing derived from the panic value reaches the client: a panic often carries a pointer
address, a SQL fragment or a file path, and a stack trace maps out the server's internals.

`http.ErrAbortHandler` is re-panicked rather than swallowed, because `net/http` uses it to
abort a response deliberately.

## Replacing the envelope

`AppOptions.ErrorRenderer` replaces the shape entirely.

```go
func renderError(ctx *muzak.Context, err error) (int, any) {
    var verr *muzak.ValidationError
    if errors.As(err, &verr) {
        return http.StatusUnprocessableEntity, problemDetails{
            Type:     "https://example.com/probs/validation",
            Title:    "Your request is not valid.",
            Status:   http.StatusUnprocessableEntity,
            Instance: ctx.RequestID(),
            Errors:   verr.Details,
        }
    }

    var coder muzak.StatusCoder
    if errors.As(err, &coder) {
        status := coder.HTTPStatus()
        return status, problemDetails{
            Type:     "about:blank",
            Title:    err.Error(),
            Status:   status,
            Instance: ctx.RequestID(),
        }
    }

    // Anything unrecognised stays opaque, exactly as the default renderer does.
    return http.StatusInternalServerError, problemDetails{
        Type:     "about:blank",
        Title:    "An internal error occurred.",
        Status:   http.StatusInternalServerError,
        Instance: ctx.RequestID(),
    }
}

app := muzak.New(muzak.AppOptions{
    Title:         "Awesome API",
    ErrorRenderer: renderError,
})
```

A renderer receives the request `Context`, so the route, the request identifier and the
headers are all available. Returning a nil body writes the status with no content.

A renderer must not leak internal state. The error it receives may be anything a handler
returned, including a wrapped database or filesystem error, so classify the errors you
recognise and fall back to an opaque response for the rest, exactly as
`muzak.DefaultErrorRenderer` does. That function is exported, so a renderer that only
wants to special-case one thing can delegate the rest to it.

## Correlating a response with the log

Every response carries `X-Request-Id`, and every error body repeats it as `request_id`.
The same value appears in the access log line and in every log record the request
produced.

```
14:32:10.114 WARN  [Request]       POST /items/  status=409  duration=412µs  bytes=181  request_id=0611f4b2
```

An error carrying a cause through `Wrap`, and every error that does not describe itself,
also produces a record of its own at error level with the real reason in it. The reason
stays in the log; only the envelope above reaches the client. A deliberate 4xx that
carries no cause is fully described by its response, so it gets no extra line.

Identifiers are generated per request and are not taken from the client by default,
because an attacker-controlled identifier is an attacker-controlled log field. See
[Logging](/docs/fundamentals/logging) for how to change that.

## Where to go next

[Middleware](/docs/getting-started/middleware) covers the chain an error travels
back out through, and [Validation](/docs/fundamentals/validation) covers the
rules behind a `422`.


--------------------------------------------------------------------------------
title: "Middleware"
description: "The chain that is already installed, the two you can add, how to instrument a service without a tracing dependency, how to write your own, and the one thing that fails quietly in Go."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/getting-started/middleware"
--------------------------------------------------------------------------------

# Middleware

Middleware in Muzak is an ordinary `func(http.Handler) http.Handler`, so anything written
for the standard library works unchanged.

```go
type Middleware func(next http.Handler) http.Handler
```

It operates below the typed layer, on the raw `net/http` types. Everything typed happens
inside `dispatch`, at the end of the chain.

## What is already installed

Four pieces of middleware are installed for you, in this order, outermost first.

| Middleware | What it does | Turn it off with |
|---|---|---|
| `RequestID` | Assigns an identifier, records it in the request context, echoes it in `X-Request-Id` | Not disableable |
| `SecurityHeaders` | Sets `X-Content-Type-Options`, `X-Frame-Options` and a referrer policy, never overwriting a value already set | `DisableSecurityHeaders` |
| `Recovery` | Catches a panic, logs it with its stack, answers a generic 500 | Not disableable |
| `AccessLog` | One line per request with method, path, status, duration, bytes and identifier | `DisableAccessLog` |
| `Locale` | Resolves the request's locale, records it in the request context, echoes it in `Content-Language` | Installed only when `I18n` names a store |

Anything you add with `App.Use` runs inside that chain, so it already has an identifier
available and is already covered by panic recovery. CORS, when configured, runs after
your middleware and before the documentation routes and the router.

```
RequestID → SecurityHeaders → Recovery → Locale → AccessLog → your middleware → CORS → /docs and /openapi.json → routes
```

`Locale` sits where it does for two reasons, and it is only there at all when
[internationalization](/docs/fundamentals/internationalization) is configured. It is inside
`Recovery` because a custom extractor is your code and can panic. It is outside `AccessLog`
because middleware further out holds the request as it arrived, so the log can only record
a locale that was resolved before it ran.

`Use` installs in order, so the first one installed is the outermost. Calls made after the
application has been built have no effect, because the chain is assembled once.

## Compression

```go
app.Use(muzak.Compress(muzak.CompressionOptions{}))
```

One line. It negotiates gzip or deflate from `Accept-Encoding`, records `Vary` on every
response either way, and declines what compressing would not help. See
[Compression](/docs/techniques/compression) for the options and the security
note that goes with it.

## CORS

CORS is configured rather than installed, and the zero value denies everything.

```go
app := muzak.New(muzak.AppOptions{
    Title: "Awesome API",
    CORS: muzak.CORSOptions{
        AllowedOrigins:   []string{"https://app.example.com"},
        AllowCredentials: true,
        MaxAge:           10 * time.Minute,
    },
})
```

No CORS header is emitted until a policy names an origin or supplies an
`AllowOriginFunc`, and a wildcard origin combined with credentials is refused as a build
error rather than served. See [CORS](/docs/security/cors).

## Instrumenting a service

Middleware is the seam for tracing and metrics, and it runs below the typed layer, before
routing. So the one thing an instrument needs -- which route matched -- is not something it
can see for itself. `RouteFromContext` publishes it.

```go
func Tracing(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()
        next.ServeHTTP(w, r)

        route, ok := muzak.RouteFromContext(r.Context())
        if !ok {
            route = "unmatched"     // a 404 is a 404, not traffic to a route named ""
        }
        status := 0
        if recorder, ok := w.(muzak.StatusRecorder); ok {
            status = recorder.Status()
        }
        record(route, status, time.Since(start))
    })
}
```

**Use the template, never the path.** `r.URL.Path` is the concrete request:

```
/orgs/01a06391-0a5b-7695-af40-babc7981a63e/apps/01a063ad-d99f-7a05-98b6-d12f11b24ea3/keys
```

As a span name or a metric label that is one time series per organization per app. Unbounded
cardinality is how a tracing backend falls over, and it makes the only question worth asking
-- how slow is this endpoint -- unanswerable, because every request is its own endpoint. The
template is bounded by the number of routes you wrote:

```
/orgs/{org_id}/apps/{app_id}/keys
```

Two rules about when to read it. **After `next.ServeHTTP` returns**, because before that
routing has not happened. And a request that matched nothing reports `false` rather than an
empty string, so a miss is counted as a miss.

`StatusRecorder` is the response writer the access log installs. The assertion can fail --
`DisableAccessLog` means there is no wrapper -- so middleware that needs the status either
way should fall back to wrapping the writer itself.

The framework carries no tracing library and no dependency for one. It has none at all, and
an OpenTelemetry API is ten modules before an SDK or an exporter is chosen. What it owes an
instrument is the fact it alone knows, which is the route; everything else belongs in the
application or beside the framework.

## The access log

```go
app := muzak.New(muzak.AppOptions{
    Title: "Awesome API",
    AccessLogOptions: muzak.AccessLogOptions{
        Level:     slog.LevelInfo,
        SkipPaths: []string{"/healthz"},
    },
})
```

```
14:32:10.114 INFO  [Request]       GET /items/  status=200  route=/items/  duration=412µs  bytes=181  request_id=0611f4b2
```

Successful responses are logged at `Level`. Server errors are always logged at error level
and client errors at warn level, so a quiet production level still surfaces failures.
`SkipPaths` lists exact paths that produce no line, which keeps a health check polled
every second from drowning out real traffic.

`route` is the template the request matched, beside the concrete path in the message. The
path is what somebody reading a log wants; the template is what a query groups by, and what
joins a log line to the trace and the metric for the same endpoint.

Only fixed, non-sensitive fields are recorded. Query strings, request bodies and headers
are deliberately omitted, because each of them routinely carries credentials or personal
data that should not be duplicated into a log store.

## Request identifiers

```go
app := muzak.New(muzak.AppOptions{
    Title:                "Awesome API",
    TrustRequestIDHeader: true,
})
```

By default an inbound `X-Request-Id` is ignored and a fresh UUID version 7 is generated,
because an attacker-controlled identifier is an attacker-controlled log field. With the
option on, an inbound value is honoured only if it parses as a UUID, so it can never carry
newlines or control characters into a log line.

Inside a handler the identifier is `ctx.RequestID()`. In code that has only a
`context.Context`, such as a repository or a client wrapper, it is
`muzak.RequestIDFromContext(ctx)`.

## Writing your own

::code-group

```go [core/middleware.go]
// ProcessTime reports how long the service spent on a request in an
// X-Process-Time header, in seconds.
func ProcessTime() muzak.Middleware {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            next.ServeHTTP(&processTimer{ResponseWriter: w, start: time.Now()}, r)
        })
    }
}

// processTimer stamps the elapsed time onto the response as it starts.
type processTimer struct {
    http.ResponseWriter
    start   time.Time
    stamped bool
}

// WriteHeader records the duration and forwards the status.
func (w *processTimer) WriteHeader(status int) {
    if !w.stamped {
        w.stamped = true
        elapsed := time.Since(w.start).Seconds()
        w.Header().Set("X-Process-Time", strconv.FormatFloat(elapsed, 'f', 6, 64))
    }
    w.ResponseWriter.WriteHeader(status)
}

// Write stamps a response whose handler never set a status, which net/http
// treats as a 200.
func (w *processTimer) Write(b []byte) (int, error) {
    if !w.stamped {
        w.WriteHeader(http.StatusOK)
    }
    return w.ResponseWriter.Write(b)
}

// Flush passes a flush through to whatever is underneath, so that installing
// this middleware cannot stop a handler from streaming.
func (w *processTimer) Flush() {
    if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
        flusher.Flush()
    }
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (w *processTimer) Unwrap() http.ResponseWriter { return w.ResponseWriter }
```

```go [cmd/main.go]
app.Use(core.ProcessTime())
app.Use(muzak.Compress(muzak.CompressionOptions{}))
```

::

### The one thing that fails quietly

The obvious shape for that middleware does not work, and it fails without a word:

```go
start := time.Now()
next.ServeHTTP(w, r)
w.Header().Set("X-Process-Time", elapsed(start)) // too late
```

That reads correctly and does nothing. Go puts the header block on the wire at the first
`WriteHeader`, which the handler has already called by the time control comes back, so the
value is written to a map nobody will read again and no error is reported. Middleware that reports something only known
at the end has to wrap the writer and fill the value in as the response starts, which is
what `processTimer` above does.

Two details make a wrapper well behaved:

- Implement `Write` as well as `WriteHeader`, because a handler that writes a body without
  setting a status never calls `WriteHeader` itself.
- Implement `Unwrap() http.ResponseWriter`, so `http.ResponseController` can still reach
  the real writer. Without it, a wrapper breaks flushing, hijacking and the deadline
  handling that WebSockets and event streams depend on.

## Middleware and the typed layer

Middleware runs before routing, so it cannot see the matched route, the bound input or the
resolved dependencies. Anything that needs those belongs in a guard or a provider, which
run per route and can refuse the request with a typed error. See
[Dependencies](/docs/getting-started/dependencies).

## Where to go next

[Lifecycle](/docs/getting-started/lifecycle) covers the resources a request
depends on, and [Compression](/docs/techniques/compression) covers the one piece
of middleware most applications add.


--------------------------------------------------------------------------------
title: "Lifecycle"
description: "Open a database pool, a cache client or a loaded model before traffic arrives, and release it after the server has drained."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/getting-started/lifecycle"
--------------------------------------------------------------------------------

# Lifecycle

Anything expensive to create, shared by every request and needing an orderly release fits
one shape.

```go
type Lifecycle interface {
    // Name identifies the component in start-up and shutdown logs. Keep it
    // short and lowercase, such as "redis" or "database".
    Name() string
    // Start acquires the resource. It is called once, before the server begins
    // accepting requests, and must return only when the resource is ready to
    // use. The context is cancelled when a sibling component fails, so a
    // long-running dial should honour it and give up.
    Start(ctx context.Context) error
    // Stop releases the resource. It is called once, after the HTTP server has
    // finished draining in-flight requests, and is called even for a start-up
    // that failed part way, for every component that did start.
    Stop(ctx context.Context) error
}
```

Publish the value with `WithSingleton` and, if it implements `Lifecycle`, Muzak takes it
from there.

## A component that implements the interface

::code-group

```go [core/models.go]
// ModelRegistry holds the prediction models the service serves.
//
// It implements muzak.Lifecycle, so publishing it with muzak.WithSingleton is
// enough for Muzak to load it before the server accepts traffic and release it
// after the server has drained.
type ModelRegistry struct {
    mu     sync.RWMutex
    models map[string]func(float64) float64
}

// NewModelRegistry returns an empty registry. The models themselves are loaded
// in Start, not here, so that construction stays cheap and failure has a place
// to be reported.
func NewModelRegistry() *ModelRegistry {
    return &ModelRegistry{models: map[string]func(float64) float64{}}
}

// Name identifies the component in start-up and shutdown logs.
func (r *ModelRegistry) Name() string { return "ml-model" }

// Start loads the models. A real implementation would read weights from disk
// or object storage and should honour ctx, which Muzak cancels as soon as a
// sibling component fails.
func (r *ModelRegistry) Start(ctx context.Context) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.models["answer_to_everything"] = func(x float64) float64 { return x * 42 }
    return nil
}

// Stop releases the models. It runs only after the HTTP server has finished
// draining, so a request that is still predicting keeps working right up to the
// end.
func (r *ModelRegistry) Stop(ctx context.Context) error {
    r.mu.Lock()
    defer r.mu.Unlock()
    clear(r.models)
    return nil
}

// Predict runs the named model and reports whether it was loaded.
func (r *ModelRegistry) Predict(name string, x float64) (float64, bool) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    model, ready := r.models[name]
    if !ready {
        return 0, false
    }
    return model(x), true
}
```

```go [cmd/main.go]
models := core.NewModelRegistry()

app := muzak.New(muzak.AppOptions{Title: "Awesome API"},
    muzak.WithSingleton(models),
)
```

```go [handlers/meta.go]
func Predict(ctx *muzak.Context, in schemas.PredictParams) (schemas.PredictOut, error) {
    models := muzak.From[*core.ModelRegistry](ctx)

    result, ready := models.Predict("answer_to_everything", in.X)
    if !ready {
        return schemas.PredictOut{}, muzak.ServiceUnavailable("the model is not loaded")
    }
    return schemas.PredictOut{Result: result}, nil
}
```

::

## A component built from two closures

A type you do not own cannot implement the interface, and a small resource does not
warrant a type of its own. `LifecycleFunc` attaches start and stop functions to a value
published with `WithSingleton`.

::code-group

```go [core/store.go]
// Lifecycle returns the option that registers the store's seeding and teardown
// with the application.
//
// The closures capture the store by pointer, so what they mutate is what
// handlers later read.
func (s *ItemStore) Lifecycle() muzak.SingletonOption {
    return muzak.LifecycleFunc("item-store",
        func(ctx context.Context) error {
            s.mu.Lock()
            defer s.mu.Unlock()
            s.items["foo"] = Item{ID: "foo", Name: "Foo"}
            s.items["bar"] = Item{ID: "bar", Name: "Bar"}
            return nil
        },
        func(ctx context.Context) error {
            s.mu.Lock()
            defer s.mu.Unlock()
            clear(s.items)
            return nil
        },
    )
}
```

```go [cmd/main.go]
store := core.NewItemStore()

app := muzak.New(muzak.AppOptions{Title: "Awesome API"},
    muzak.WithSingleton(store, store.Lifecycle()),
)
```

::

The value itself is published unchanged, so the handler still retrieves it by type with
`muzak.From[*core.ItemStore](ctx)`. The closures must capture something whose contents
can be mutated in place, such as a map, a struct pointer or a slice header held behind
one. Reassigning a captured variable inside `Start` will not change what handlers see.

`muzak.NewLifecycle(name, start, stop)` builds a standalone `Lifecycle` from the same two
closures, for a component that is not published as a value at all. Either function may be
nil, which makes that half a no-op.

## Components with no published value

A background worker or a metrics exporter has nothing a handler would ask for. Register it
directly.

```go
app := muzak.New(muzak.AppOptions{Title: "Awesome API"},
    muzak.WithLifecycle(
        muzak.NewLifecycle("outbox-worker", worker.Start, worker.Stop),
    ),
)
```

Components registered this way are started and stopped exactly like those discovered
through `WithSingleton`.

## Start-up

Components start **in parallel**, so start-up costs the slowest one rather than the sum.

```
14:32:07.482 INFO  [Server]        Starting Muzak application...
14:32:07.483 INFO  [Server]        Starting 3 lifecycle components in parallel: redis, database, ml-model
14:32:07.501 INFO  [Server]        Started "redis" (18ms)
14:32:07.512 INFO  [Server]        Started "ml-model" (29ms)
14:32:07.544 INFO  [Server]        Started "database" (61ms)
14:32:07.545 INFO  [Server]        All lifecycle components ready (61ms total)
```

If one fails, the others are cancelled immediately through the context they were given,
everything that did start is stopped, and the failures are reported together. A failed
start-up never leaks a connection pool.

That is why `Start` should honour its context. A dial that ignores cancellation holds up a
start-up that has already failed.

## Shutdown

Shutdown runs in the order that matters:

1. Open WebSocket connections are closed with `1001 Going Away`, and open event streams are
   ended. Both are tracked by the application precisely so this can happen: a hijacked
   connection is no longer one `net/http` knows about, and a streaming handler would
   otherwise make the drain wait out its whole deadline once per stream.
2. The HTTP server stops accepting new connections and drains its in-flight requests.
3. Only then are the lifecycle components stopped.

Pulling a database connection out from under a request that is still running would turn an
orderly shutdown into a burst of errors, which is why components stay usable to the very
end.

`ServerOptions.ShutdownTimeout` bounds the wait for in-flight requests, defaulting to 15
seconds. Set it below whatever grace period the platform allows, or the platform kills a
drain that is still running: Cloud Run allows about ten seconds after `SIGTERM`, and
Kubernetes and ECS both default to thirty.

### A component that panics

A panic in `Start` or `Stop` becomes that component's error rather than the end of the
process, and the components either side of it are still started or released.

This matters most in `Stop`, which runs after the drain, on the framework's goroutine,
when every request has already been answered. A crash there takes the process down at the
one moment where doing so achieves nothing and the stack points at the framework rather
than at the component that caused it. A handler that panics already becomes a 500 rather
than a dead process; this is the same bargain for the same reason.

It is a backstop, not permission. A component whose `Stop` can panic is usually one that
dereferences something `Start` never set, and the fix is to check it:

```go [core/deps.go]
func (d *Deps) Stop(ctx context.Context) error {
    // Every field is checked, because a partially constructed Deps is exactly
    // what exists when the constructor failed halfway.
    if d.Pool != nil {
        d.Pool.Close()
    }
    if d.Cache != nil {
        return d.Cache.Close(ctx)
    }
    return nil
}
```

## Driving it by hand

`Run`, `RunContext` and `RunSignals` call `StartLifecycle` between building the application
and opening the socket, and `Shutdown` calls `StopLifecycle` after the server has drained.
A program that serves the application some other way, such as a test wiring it into
`httptest`, calls them itself.

```go
if err := app.Build(); err != nil {
    return err
}
if err := app.StartLifecycle(ctx); err != nil {
    return err
}
defer app.StopLifecycle(context.Background())
```

Both are idempotent: starting twice is a no-op, and stopping components that were never
started returns nil.

The `muzak.dev/framework/testclient` package does all of this for you, including releasing everything
through the test's cleanup. See [Testing](/docs/fundamentals/testing).

## Components the framework registers

`MemoryRateLimitStorage` implements `Lifecycle`, so an application that uses the built-in
rate limiter starts and stops it as part of its own start-up and shutdown. Stopping
releases every counter it holds, so no key outlives the server that was counting it. The
same applies to a `RateLimitStorage` of your own: implement `Lifecycle` and it is started
before serving and stopped after draining, with no separate registration.

## Where to go next

[Configuration](/docs/fundamentals/configuration) covers reading the settings
those components are built from, and
[Server Configuration](/docs/deployment/server-configuration) covers the
shutdown timeout and the signals that trigger it.


--------------------------------------------------------------------------------
title: "Configuration"
description: "Read settings from the environment and a dotenv file into a typed struct, with every missing or malformed variable reported together."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/fundamentals/configuration"
--------------------------------------------------------------------------------

# Configuration

Configuration is a struct. Its tags say where each field comes from and what happens when
the value is absent, and the whole thing is read once at start-up.

::code-group

```go [core/config.go]
// Settings is the service configuration, read once at start-up from the
// environment and from a .env file when one is present.
type Settings struct {
    // AppName titles the API in the generated documentation.
    AppName string `env:"APP_NAME" default:"Awesome API"`
    // AdminEmail is who to contact about the API.
    AdminEmail string `env:"ADMIN_EMAIL" required:"true"`
    // ItemsPerUser caps how many items one user may hold.
    ItemsPerUser int `env:"ITEMS_PER_USER" default:"50"`
    // Addr is the address the server listens on.
    Addr string `env:"ADDR" default:":8080"`
    // AdminToken guards the admin subtree. It is marked secret so that a
    // malformed value never appears in a start-up error.
    AdminToken string `env:"ADMIN_TOKEN" secret:"true" required:"true"`
    // TrustedProxies lists the proxies whose X-Forwarded-For header is
    // believed, as a comma-separated list of addresses or CIDR prefixes.
    TrustedProxies []string `env:"TRUSTED_PROXIES"`
}

// LoadSettings reads the configuration, stopping the process if a required
// value is missing.
func LoadSettings() Settings {
    return muzak.MustLoadConfig[Settings](muzak.EnvFile(".env"))
}
```

```go [cmd/main.go]
settings := core.LoadSettings()

app := muzak.New(muzak.AppOptions{
    Title:   settings.AppName,
    Version: "1.0.0",
    Contact: &muzak.Contact{Email: settings.AdminEmail},
    Addr:    settings.Addr,
},
    muzak.WithSingleton(settings),
)
```

::

Publishing the value with `WithSingleton` is what lets a handler read it with
`muzak.From[core.Settings](ctx)` rather than through a package-level variable.

## Loading

```go
settings, err := muzak.LoadConfig[Settings](muzak.EnvFile(".env"))
if err != nil {
    return err
}
```

`MustLoadConfig` is the same thing for a program that cannot run without its
configuration. It panics, which is the right behaviour in a `main` function: a service
missing a required setting should stop immediately and visibly rather than start in an
undefined state. Prefer `LoadConfig` anywhere the failure can be handled.

Every problem found is reported together:

```
muzak: configuration could not be loaded:
muzak: ADMIN_EMAIL is required but was not set in the environment or .env
muzak: ITEMS_PER_USER must be a valid integer (got "fifty")
muzak: ADMIN_TOKEN could not be parsed (value hidden because the field is marked secret)
```

A first run in a new environment lists everything missing at once instead of one thing per
attempt.

## The tags

| Tag | Effect |
|---|---|
| `env:"NAME"` | The variable to read. Without it, the name is derived from the field |
| `env:"-"` | Skip the field entirely |
| `default:"value"` | The value used when no source holds the variable |
| `required:"true"` | An absent variable is an error |
| `secret:"true"` | Keep the value out of the error produced when it fails to parse |

A field with neither `default` nor `required:"true"` is simply left at its zero value when
nothing supplies it.

### Derived names

Without an `env` tag, the name is the field name upper-cased with underscores between
words:

| Field | Variable |
|---|---|
| `AppName` | `APP_NAME` |
| `ItemsPerUser` | `ITEMS_PER_USER` |
| `DatabaseURL` | `DATABASE_URL` |
| `APIKey` | `API_KEY` |

### Secrets

`secret:"true"` matters for the error path. A malformed value normally appears in the
message, which is what makes a typo obvious, but a malformed credential in a start-up log
is a credential in a log.

```go
DatabaseURL string `env:"DATABASE_URL" secret:"true" required:"true"`
```

That applies to a custom type's own error too. A type implementing
`encoding.TextUnmarshaler` writes its own message, which may well echo the text it was
given, so a secret field's failure is replaced wholesale rather than filtered.

## Types

Fields are converted with the same setters the request binder uses, so configuration and
requests agree on what an `int` is:

- `string`, `bool`, every integer width, `float32` and `float64`
- `time.Duration`, written as `1500ms`, `30s`, `1h30m`
- any type implementing `encoding.TextUnmarshaler`, which covers `time.Time` and
  `uuid.UUID`
- a slice of any of the above, written as a comma-separated list

```go
type Settings struct {
    Addr           string        `env:"ADDR" default:":8080"`
    ReadTimeout    time.Duration `env:"READ_TIMEOUT" default:"30s"`
    Workers        int           `env:"WORKERS" default:"4"`
    Debug          bool          `env:"DEBUG" default:"false"`
    AllowedOrigins []string      `env:"ALLOWED_ORIGINS"`
}
```

```bash
ALLOWED_ORIGINS=https://app.example.com,https://admin.example.com
```

Entries are split on commas and trimmed of surrounding spaces. An empty variable yields an
empty slice rather than a slice holding one empty string.

## Composing settings

An embedded struct is flattened, so a group of settings several services share is declared
once and reused.

```go
type DatabaseSettings struct {
    URL         string        `env:"DATABASE_URL" required:"true" secret:"true"`
    MaxConns    int           `env:"DATABASE_MAX_CONNS" default:"10"`
    DialTimeout time.Duration `env:"DATABASE_DIAL_TIMEOUT" default:"5s"`
}

type Settings struct {
    DatabaseSettings

    AppName string `env:"APP_NAME" default:"Awesome API"`
    Addr    string `env:"ADDR" default:":8080"`
}
```

`settings.URL` and `settings.AppName` both read as if declared on `Settings`.

## Sources

The process environment is consulted first, then each source in the order it was added,
and the first source holding a name wins. That ordering is what lets a deployed value beat
a checked-in file.

```go
settings, err := muzak.LoadConfig[Settings](
    muzak.EnvFile(".env"),
    muzak.EnvPrefix("AWESOME_"),
)
```

| Option | What it adds |
|---|---|
| `EnvFile(path)` | A dotenv file. A missing file is not an error; a malformed one is |
| `ConfigValues(map)` | An explicit set of values, which is what a test uses |
| `WithConfigSource(src)` | Anything implementing `ConfigSource`, such as a secret manager |
| `EnvPrefix(prefix)` | Requires every name to carry the prefix, so `APP_NAME` is read as `AWESOME_APP_NAME` |
| `WithoutEnvironment()` | Stops the process environment being consulted at all |

### The dotenv format

```bash [.env]
# Copy to .env and adjust. Values exported in the real environment always win
# over this file, so a container runtime can override anything here.

APP_NAME=Awesome API
ADMIN_EMAIL=admin@example.com
ITEMS_PER_USER=50
ADDR=:8080

# Guards the /admin subtree. Compared in constant time.
ADMIN_TOKEN=coneofsilence

export DATABASE_URL="postgres://localhost:5432/awesome?sslmode=disable"

# Proxies whose X-Forwarded-For header is believed, as a comma-separated list.
# TRUSTED_PROXIES=10.0.0.0/8,172.16.0.0/12
```

One `KEY=VALUE` pair per line. Blank lines and lines beginning with `#` are ignored, an
optional leading `export ` is stripped, and a value may be wrapped in single or double
quotes to preserve surrounding spaces or a `#`. Escape sequences are interpreted only
inside double quotes.

Keep the file out of version control and check in a `.env.example` beside it.

## Configuration in tests

Pin the values a test needs and shut the environment out, so a test does not inherit
whatever the developer happens to have exported.

```go [core/config_test.go]
func TestLoadSettings(t *testing.T) {
    settings, err := muzak.LoadConfig[core.Settings](
        muzak.WithoutEnvironment(),
        muzak.ConfigValues(map[string]string{
            "ADMIN_EMAIL": "admin@example.com",
            "ADMIN_TOKEN": "coneofsilence",
        }),
    )
    if err != nil {
        t.Fatalf("LoadConfig = %v", err)
    }
    if settings.ItemsPerUser != 50 {
        t.Errorf("ItemsPerUser = %d, want the default 50", settings.ItemsPerUser)
    }
}
```

## A source of your own

```go
type vaultSource struct {
    client *vault.Client
    path   string
}

func (v vaultSource) Name() string { return "vault:" + v.path }

func (v vaultSource) Lookup(key string) (string, bool) {
    secret, err := v.client.Read(v.path + "/" + key)
    if err != nil || secret == nil {
        return "", false
    }
    return secret.Value, true
}
```

```go
settings, err := muzak.LoadConfig[Settings](
    muzak.WithConfigSource(vaultSource{client: client, path: "secret/awesome"}),
    muzak.EnvFile(".env"),
)
```

`Name` appears in the error listing the sources that were searched. A present but empty
value must be reported as present, so that an explicitly blank setting can override a
default.

## Where to go next

[Lifecycle](/docs/getting-started/lifecycle) covers building resources from
those settings, and [Logging](/docs/fundamentals/logging) covers the logger the
application uses while it does.


--------------------------------------------------------------------------------
title: "Validation"
description: "Rules declared against the field itself, transforms that mutate before the checks run, cross-field conditions in ordinary Go, and constraints that reach the OpenAPI document."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/fundamentals/validation"
--------------------------------------------------------------------------------

# Validation

An input model declares its rules by implementing one method.

```go
type Validatable interface {
    Validate(v *muzak.Validation)
}
```

```go [schemas/users.go]
func (in *CreateUserIn) Validate(v *muzak.Validation) {
    v.String(&in.Email).Trim().Lower().Required().Email()
    v.String(&in.Password).Required().MinLen(12).Must(NotACommonPassword)
    v.String(&in.Confirm).Equal(in.Password).Message("must match the password")
    v.Number(&in.Age).Between(18, 120)
    v.String(&in.Role).OneOf("admin", "editor", "viewer")
    v.Slice(&in.Tags).MaxItems(10).Unique().Each(validate.String().MaxLen(20))
}
```

Implementing it is the only thing needed. There is no option to remember and no pipe to
install, so a model cannot be left unvalidated by forgetting one. The method belongs on
the pointer type, which is what lets rules name fields by address.

`&in.Email` **is** the field, so renaming it is a change the compiler checks, and
`v.Number(&in.Email)` does not compile. Type `v.` and your editor lists the kinds; type
`v.String(&in.Email).` and it lists every rule that applies to a string.

## How a rule set behaves

Four things govern every rule set:

- **Transforms mutate, and run first.** `Trim().Lower()` changes what the handler receives.
  Because they run before the checks, `Trim().Required()` rejects a field of pure
  whitespace.
- **Optional means optional.** A field without `Required()` skips its remaining checks when
  it is empty, so `MaxLen(20)` has nothing to say about a value nobody sent. A nil pointer
  field is skipped entirely.

  **A number is the exception, and it is narrower.** Zero only counts as empty when the
  rules would have accepted zero anyway. `Between(1, 720)` applies to `0` and rejects it,
  because a rule set that says zero is out of range should not be the one thing that lets
  it through. See [Number rules](#number-rules).
- **One failure per field.** The first check that fails reports; a single empty field does
  not also complain that it is too short and not an email address.
- **Guards run first.** Validation happens after binding and after every guard, so an
  unauthenticated caller gets `401`, not a map of your schema.
- **A rule binds to the field, not to what the field holds.** `v.String(&in.Name)` is
  right for `Name string` and for `Name *string` alike -- the address is how a failure
  finds the name it reports under. `v.String(in.Name)` compiles, because the entry points
  accept the pointer type, and is refused when the route is compiled: it would report
  failures with no field name and drop its constraints from the generated document.

## The entry points

| Call | Field types | Rule set |
|---|---|---|
| `v.String(&in.F)` | `string`, `*string` | `validate.StringRules` |
| `v.Number(&in.F)` | any integer, float, or pointer to one | `validate.NumberRules` |
| `v.Slice(&in.F)` | `[]E` | `validate.SliceRules[E]` |
| `v.Time(&in.F)` | `time.Time`, `*time.Time` | `validate.TimeRules` |
| `v.Value(&in.F)` | any type | `validate.ValueRules[T]` |
| `v.Nested(&in.F)` | another `Validatable` | |
| `v.When(cond)` | | a cross-field condition |
| `v.Reject(&in.F, issue)` | | a failure recorded directly |

## String rules

```go
v.String(&in.Username).Trim().Lower().Required().MinLen(2).MaxLen(32).
    Matches(`^[a-z0-9_]+$`).
    Message("may only contain lower case letters, digits and underscores").
    NotOneOf("admin", "root", "support").
    Message("is reserved")
```

| Rule | Effect | Message on failure |
|---|---|---|
| `Trim()` | Transform: removes surrounding whitespace | |
| `Lower()` / `Upper()` | Transform: folds case | |
| `Required()` | Rejects an empty value | `is required` |
| `MinLen(n)` / `MaxLen(n)` / `Len(n)` | Bounds the length in runes | `must be at least 12 characters` |
| `Email()` | Parses with `net/mail` | `must be a valid email address` |
| `URL()` | Requires an absolute http or https URL with a host | `must be a valid absolute http or https URL` |
| `UUID()` | Any of the usual spellings | `must be a valid UUID` |
| `Matches(pattern)` | A regular expression | `is not in the expected format` |
| `OneOf(...)` | A fixed set, which also becomes the enum in the document | `must be one of "admin", "editor", "viewer"` |
| `NotOneOf(...)` | A set the value must never take | `is not available` |
| `Prefix(s)` / `Suffix(s)` / `Contains(s)` | Substring checks | `must begin with "user_"` |
| `Equal(other)` / `EqualFold(other)` | Matches another value, the second ignoring case | `does not match` |
| `MatchesNot(pattern)` | Rejects a regular expression, for a shape easier to name than the shapes it excludes | `is in a format that is not accepted` |
| `NotBlank()` | Rejects a value that is nothing but whitespace, and an absent one | `must not be blank` |
| `NoControl()` | Rejects control characters, including tabs and line breaks | `must not contain control characters` |
| `MinBytes(n)` / `MaxBytes(n)` | Bounds the length in bytes rather than in runes | `must be at least 8 bytes` |
| `Must(fn)` | Your own `func(string) error` | whatever the function returns |
| `As(name)` | Renames the field in this rule set's messages | |
| `Message(text)` | Overrides the wording of the check written immediately before it | |
| `MessageKey(key, args...)` | Overrides it with a translation key instead, so the override is translated like every built-in rule | |

Lengths are counted as runes rather than bytes, so a name in any script is measured the
way a person would count it.

`Matches` compiles its expression when the rule is declared, so a malformed pattern is a
panic at start-up rather than a failure on the first request that happens to reach it.

`URL()` rejects every scheme but http and https on purpose. Without that restriction,
`javascript:`, `data:` and `file:` all parse as perfectly valid absolute URLs, and a value
this check approves is exactly the kind of thing that ends up in a redirect, a fetch or an
href with the validator's blessing.

### Addresses and URLs

`URL()` accepts any absolute http or https URL. These narrow it, which is
usually what a field means.

| Rule | Effect | Message on failure |
|---|---|---|
| `HTTPS()` | An absolute https URL with a host | `must be a valid https URL` |
| `URLWithSchemes(...)` | An absolute URL using one of the given schemes | `must be a URL using "s3" or "gs"` |
| `Host()` | A DNS hostname, with the label rules a resolver enforces | `must be a valid hostname` |
| `IP()` / `IPv4()` / `IPv6()` | An address of either family, or of one | `must be a valid IPv4 address` |
| `CIDR()` | A network, as `10.0.0.0/8` | `must be a valid network in CIDR notation` |
| `MAC()` | A hardware address in any of its spellings | `must be a valid MAC address` |

`URL()` refuses every scheme but http and https on purpose. Without a
restriction, `javascript:`, `data:` and `file:` all parse as perfectly valid
absolute URLs, and a value a validator has blessed is exactly the kind of thing
that ends up in a redirect or an `href`.

`IPv6()` refuses an IPv4 address written in the mapped form, such as
`::ffff:127.0.0.1`. It is an IPv4 address wearing a costume, and a field asking
for IPv6 wants one an IPv6-only network can route.

### Character classes and formats

| Rule | Effect | Message on failure |
|---|---|---|
| `Alpha()` | Letters only | `must contain only letters` |
| `Alphanumeric()` | Letters and digits | `must contain only letters and digits` |
| `Numeric()` | Digits only, as characters rather than as a number | `must contain only digits` |
| `ASCII()` | Printable ASCII, which is space through tilde | `must contain only printable ASCII characters` |
| `Slug()` | Lower case letters and digits in hyphen-separated groups | `must contain only lower case letters, digits and hyphens` |
| `Hex()` | Hexadecimal digits, in either case | `must be hexadecimal` |
| `HexColour()` | A colour, as `#1a2b3c`, with three, four, six or eight digits | `must be a hexadecimal colour, such as #1a2b3c` |
| `Base64()` | Decodes as standard base64 | `must be valid base64` |
| `JSON()` | A well-formed JSON document, for a field carrying JSON as text | `must be valid JSON` |
| `Semver()` | A semantic version, as `2.0.0-rc.1+build.5` | `must be a semantic version, such as 1.4.0` |
| `E164()` | A telephone number in international format | `must be a telephone number in international format` |
| `LanguageTag()` | A BCP 47 tag, as `pt-BR` | `must be a valid language tag, such as pt-BR` |
| `Timezone()` | A zone the host knows, as `Europe/Istanbul` | `must be a known time zone, such as Europe/Istanbul` |
| `CountryCode()` / `CurrencyCode()` | An assigned ISO 3166-1 alpha-2 or ISO 4217 code | `must be a known country code` |

Letters are Unicode letters rather than the twenty-six of English, so `Alpha()`
accepts a name in any script. Narrow it by composing: `Alpha().ASCII()`.

`CountryCode()` and `CurrencyCode()` look the value up in the assigned registers
rather than counting letters, so `XQ` is refused although it is two upper case
letters, and a currency withdrawn from the standard is refused although it was
valid a few years ago.

Both tables carry the date they were taken, and both need regenerating when the
standards change. The generated document describes the shape rather than the
table, because two hundred values in every schema that names a country would be
noise rather than documentation.

Reach for `OneOf(...)` with your own list when a service trades in a known
handful. It is narrower, it becomes an enum in the document, and it cannot go
stale in a way that matters.

`Timezone()` resolves the name against the zone data the host or the binary
carries, so what passes here is exactly what `time.LoadLocation` will later
accept. Names that resolve are remembered, and a value that could not be one is
rejected on its shape first, so a client sending nonsense pays for a scan of the
string rather than for a search of the zone data.

## Number rules

```go
v.Number(&in.Age).Required().Between(18, 120)
v.Number(&in.Limit).Clamp(1, 100)
v.Number(&in.Quantity).Positive().MultipleOf(12)
```

| Rule | Effect | Message on failure |
|---|---|---|
| `Required()` | Rejects a zero value | `is required` |
| `Min(n)` / `Max(n)` / `Between(lo, hi)` | Inclusive bounds | `must be between 18 and 120` |
| `GreaterThan(n)` / `LessThan(n)` | Exclusive bounds, which refuse the bound itself | `must be greater than 10` |
| `Positive()` / `Negative()` | Compared against zero, which is refused | `must be greater than zero` |
| `NonNegative()` / `NonPositive()` | Compared against zero, which is allowed | `must not be negative` |
| `MultipleOf(n)` | For a quantity that only makes sense in steps | `must be a multiple of 12` |
| `Whole()` | Nothing after the decimal point, for a float carrying a count | `must be a whole number` |
| `Port()` | A whole number from 1 to 65535 | `must be a port number between 1 and 65535` |
| `OneOf(...)` | A fixed set, which also becomes the enum in the document | `must be one of 1 or 2` |
| `Clamp(lo, hi)` | Transform: pulls the value inside the range instead of rejecting it | |
| `Must(fn)` | Your own `func(float64) error` | whatever the function returns |

Bounds are written as ordinary untyped constants whatever the field's own numeric type, so
`Between(18, 120)` reads the same on an `int`, an `int64` and a `float64`.

The inclusive and exclusive pairs are worth keeping apart. A price that must be above zero
and a quantity that may be zero are different rules, and the generated document says so:
`GreaterThan` and `Positive` produce `exclusiveMinimum`, where `Min` and `NonNegative`
produce `minimum`. A client reading `minimum: 0` is being told that zero is allowed.

### Zero, absence, and which one you meant

Zero is indistinguishable from absent for a plain numeric field, and the two need
different answers. So the rule is narrow: **zero skips the remaining checks only when
those checks would have accepted zero.**

```go
v.Number(&in.Window).Between(1, 720)   // 0 is rejected: the bounds exclude it
v.Number(&in.Discount).Between(0, 100) // 0 is accepted: the bounds admit it
v.Number(&in.Retries).Max(10)          // 0 is accepted
v.Number(&in.Score).Must(inHouseRule)  // 0 skips: your rule is never run speculatively
```

The first line is the one that used to surprise people. Before 0.2.7 it accepted `0`,
the request reached the database, and a `CHECK` constraint turned what should have been a
`422` into a `500`. It also made this page's own promise false in the generated document,
which advertised a `minimum` the server did not enforce.

**A number that may legitimately be absent, and whose bounds exclude zero, is a pointer.**
That is the same rule that already applied to one that may legitimately *be* zero:

```go
Age *int `json:"age,omitzero"`

v.Number(&in.Age).Between(18, 120)  // nil is skipped; 0 is rejected; 25 is checked
```

`Required()` still means what it did: it demands presence, and on a plain numeric field it
rejects zero. Reach for it when the bounds themselves admit zero and you still want the
field supplied.

`Clamp` is for a value outside the range being a client being imprecise rather than a
client being wrong, such as a page size that should quietly cap rather than fail.

## Slice rules

```go
v.Slice(&in.Tags).MaxItems(10).Unique().Each(validate.String().MaxLen(20))
v.Slice(&in.Scores).MinItems(1).Each(validate.Value[int]().Must(positive))
```

| Rule | Effect | Message on failure |
|---|---|---|
| `Required()` | Rejects an empty or absent collection | `is required` |
| `MinItems(n)` / `MaxItems(n)` / `Items(n)` | Bounds the length, or fixes it | `must have at most 10 items` |
| `NotEmpty()` | Rejects an empty or absent collection, in words a client can act on | `must not be empty` |
| `Contains(v)` / `Excludes(v)` | An element that must be present, or must not be | `must contain read` |
| `Unique()` | Every element differs, compared with `reflect.DeepEqual` | `must not repeat fine` |
| `Each(rules)` | Applies a rule set to every element | reported against the position |
| `Must(fn)` | Your own `func([]E) error` | whatever the function returns |

`Each` only accepts a rule set for the element's own type, so `Each(validate.String())` on
a slice of integers does not compile. A failure inside an element is reported against that
position, as `tags[2]`, so a client can tell which one to fix.

## Time rules

```go
v.Time(&in.StartsAt).Required().After(time.Now())
v.Time(&in.BornOn).Before(eighteenYearsAgo)
v.Time(&in.Window).Between(seasonStart, seasonEnd)
```

| Rule | Effect | Message on failure |
|---|---|---|
| `Required()` | Rejects the zero time | `is required` |
| `Before(t)` / `After(t)` / `Between(a, b)` | Bounds against a moment you name | `must be before 2026-01-01T00:00:00Z` |
| `Past()` / `Future()` | Bounds against now, judged when the request is validated | `must be in the past` |
| `Within(d)` | No further from now than a distance, in either direction | `must be within 5m0s of now` |
| `Must(fn)` | Your own `func(time.Time) error` | whatever the function returns |

The generated schema carries `"format": "date-time"`.

`Within` is the rule a signed request or a replayed event wants: a timestamp far in the
past is stale and one far in the future is a clock that cannot be trusted, and both are the
same mistake.

```go
v.Time(&in.Timestamp).Within(5 * time.Minute)
```

## Value rules

`v.Value` is the escape hatch for anything the typed rule sets do not cover: a custom
type, an enum with its own `String` method, a struct compared as a whole. Because it is
generic over the field's type, `Must` and `OneOf` take that type directly and the compiler
checks the values written at the call site.

```go
type Currency string

v.Value(&in.Currency).Required().OneOf(Currency("EUR"), Currency("USD"), Currency("TRY"))
v.Value(&in.Coordinates).Must(insideServiceArea)
```

## Rules of your own

A rule is an ordinary function, so it needs no registration and is testable on its own.

```go [schemas/rules.go]
// commonPasswords stands in for the list a real service would load. Keeping it
// here rather than inside the rule means the rule stays a plain function that a
// test can call directly.
var commonPasswords = map[string]bool{
    "password1234":  true,
    "qwertyuiop12":  true,
    "letmeinplease": true,
}

// NotACommonPassword rejects a password from the known-bad list.
//
// Its message is phrased to read after the field name, the way every built-in
// rule words its own failures.
func NotACommonPassword(password string) error {
    if commonPasswords[strings.ToLower(password)] {
        return errors.New("is too common, choose something less guessable")
    }
    return nil
}
```

```go
v.String(&in.Password).Required().MinLen(12).Must(NotACommonPassword)
```

```go [schemas/rules_test.go]
func TestNotACommonPassword(t *testing.T) {
    if err := NotACommonPassword("password1234"); err == nil {
        t.Error("want a common password to be rejected")
    }
}
```

## Reusing a rule set

`validate.String()`, `validate.Number()`, `validate.Slice[E]()`, `validate.Time()` and
`validate.Value[T]()` build unbound rule sets, which is how a constraint stays consistent
across the models that share it.

```go [schemas/rules.go]
// validateTag returns the rules every tag must satisfy.
func validateTag() *validate.StringRules {
    return validate.String().Trim().Lower().MaxLen(20).
        Matches(`^[a-z0-9-]+$`).
        Message("may only contain lower case letters, digits and hyphens")
}
```

```go
v.Slice(&in.Tags).MaxItems(10).Unique().Each(validateTag())
```

An unbound rule set is also usable directly, which is what makes it testable without a
request:

```go
if err := validateTag().Check("Not A Tag"); err == nil {
    t.Error("want a spaced tag to be rejected")
}
```

## Cross-field rules

`in` is right there, so a cross-field rule is ordinary Go.

```go
// A confirmation field.
v.String(&in.Confirm).Equal(in.Password).Message("must match the password")

// A condition that reads better as a rule.
v.When(in.Role == "admin" && in.Age < 21).
    Reject(&in.Role, "an admin must be at least 21")

// The same, naming a translation rather than fixing the wording in English.
v.When(in.Price < in.Cost).
    RejectKey(&in.Price, "errors.item.price_below_cost")

// A condition that reads better as an if.
if in.Start.After(in.End) {
    v.Reject(&in.End, "must not be before the start")
}
```

`When` returns a condition, and several failures can hang off one test:

```go
v.When(in.Recurring).
    Reject(&in.Interval, "is required for a recurring booking").
    Reject(&in.EndsAt, "is required for a recurring booking")
```

## Messages in another language

Every rule's wording is translated when the application has a locale configured, and
nothing in the model says so. A failure reports the rule it came from as well as its
English, and the wording is looked up under that rule at render time.

```yaml [core/locales/es.yml]
es:
  errors:
    messages:
      blank: "es obligatorio"
      too_short:
        one: "debe tener al menos 1 carácter"
        other: "debe tener al menos %{count} caracteres"
```

`Message` fixes the wording in one language on purpose, and clears the rule with it: a
sentence you wrote is the sentence you meant. `MessageKey` names a translation instead, and
the rule's own values still fill it in, so `%{count}` works exactly as it does in the
built-in message.

```go
v.String(&in.Password).MinLen(12).MessageKey("errors.password.too_short")
```

The full picture, including the four scopes a message is looked up under, is in
[Internationalization](/docs/fundamentals/internationalization).

## Nested models

```go
type CreateOrderIn struct {
    Customer CustomerIn `json:"customer"`
    Address  AddressIn  `json:"address"`
}

func (in *CreateOrderIn) Validate(v *muzak.Validation) {
    v.Nested(&in.Customer)
    v.Nested(&in.Address)
}

func (in *AddressIn) Validate(v *muzak.Validation) {
    v.String(&in.City).Trim().Required().MaxLen(64)
    v.String(&in.PostCode).Trim().Upper().Required().Matches(`^[0-9]{5}$`)
}
```

A failure on the nested model's `City` field is reported as `address.city`. A nil pointer
is skipped, so an optional nested model needs no guard of its own.

## Validating what did not come from a body

Rules bind to the field, not to the location, so a header is checked exactly as a query
parameter is and its failure is reported against the name the client actually sent.

```go [schemas/feed.go]
func (in *FeedIn) Validate(v *muzak.Validation) {
    v.String(&in.SaveData).Trim().Lower().OneOf("on", "off")
    v.String(&in.Traceparent).Matches(`^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`).
        Message("must be a W3C trace context, such as 00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
    v.Slice(&in.Tags).MaxItems(5).Each(validate.String().MaxLen(24))
    v.String(&in.SessionID).Trim().MinLen(4).MaxLen(64)
    v.Number(&in.Limit).Between(1, 100)
}
```

Transforms run before the checks, which is what lets `Save-Data` be compared against a
single spelling in the handler afterwards.

## What the client sees

Rule failures merge into the same envelope as binding failures, with the location the
binder already established, and every field is reported at once.

```json
{
  "error": {
    "code": "validation_error",
    "message": "The request could not be validated.",
    "status": 422,
    "details": [
      { "field": "username", "location": "body",  "issue": "is reserved" },
      { "field": "email",    "location": "body",  "issue": "must be a valid email address" },
      { "field": "password", "location": "body",  "issue": "must be at least 12 characters" },
      { "field": "tags",     "location": "body",  "issue": "must not repeat fine" },
      { "field": "limit",    "location": "query", "issue": "must be between 1 and 100" }
    ]
  },
  "request_id": "0611f4b2-2f0a-4b57-9c1a-6e6a2e2f9b31"
}
```

The field name is derived from the same struct tag the binder used, so a query parameter
and a body member are reported the same way. `As(name)` overrides it for a field whose tag
reads worse than its purpose:

```go
v.String(&in.DOB).As("date_of_birth").Required()
```

## Rules feed the documentation

The constraints a rule set declares become JSON Schema keywords, so the OpenAPI document
carries the limits the code actually enforces.

| Rule | Keyword |
|---|---|
| `Required()` | membership of `required` |
| `MinLen` / `MaxLen` | `minLength` / `maxLength` |
| `Min` / `Max` / `Between` | `minimum` / `maximum` |
| `MultipleOf` | `multipleOf` |
| `MinItems` / `MaxItems` | `minItems` / `maxItems` |
| `Unique()` | `uniqueItems` |
| `OneOf(...)` | `enum` |
| `Matches(p)` | `pattern` |
| `Email()` / `URL()` / `UUID()` | `format` of `email`, `uri`, `uuid` |
| `Each(...)` | the array's `items` |

```json
"role": { "type": "string", "enum": ["admin", "editor", "viewer"] },
"tags": {
  "type": "array", "maxItems": 10, "uniqueItems": true,
  "items": { "type": "string", "maxLength": 20, "pattern": "^[a-z0-9-]+$" }
}
```

The document cannot drift from the validation, because both are read from the same
declaration. A `Must` rule is opaque by nature and contributes nothing.

## Turning validation off for a route

```go
r.Post("/admin/repair", handlers.RepairRecord, muzak.SkipValidation())
```

Reach for this only where a route must accept input the model itself would reject, such as
an administrative endpoint that repairs bad data.

## The full example

::code-group

```go [schemas/users.go]
// CreateUserIn is the JSON body for creating a user.
//
// It is the fullest example in this service: transforms, a custom rule, a
// cross-field comparison and a collection with per-element rules.
type CreateUserIn struct {
    Username string   `json:"username" doc:"Lower case letters, digits and underscores"`
    Email    string   `json:"email" doc:"Where we reach the user"`
    Password string   `json:"password" doc:"At least 12 characters"`
    Confirm  string   `json:"confirm_password"`
    Age      int      `json:"age"`
    Role     string   `json:"role" doc:"One of admin, editor or viewer"`
    Tags     []string `json:"tags,omitzero"`
    Website  *string  `json:"website,omitzero"`
}

// Validate declares what a valid signup looks like.
func (in *CreateUserIn) Validate(v *muzak.Validation) {
    v.String(&in.Username).Trim().Lower().Required().MinLen(2).MaxLen(32).
        Matches(`^[a-z0-9_]+$`).
        Message("may only contain lower case letters, digits and underscores").
        NotOneOf("admin", "root", "support").
        Message("is reserved")

    v.String(&in.Email).Trim().Lower().Required().Email()

    v.String(&in.Password).Required().MinLen(12).Must(NotACommonPassword)

    v.String(&in.Confirm).Equal(in.Password).Message("must match the password")

    v.Number(&in.Age).Required().Between(18, 120)

    v.String(&in.Role).Required().OneOf("admin", "editor", "viewer")

    v.Slice(&in.Tags).MaxItems(10).Unique().Each(validateTag())

    v.String(&in.Website).URL()

    // A cross-field rule is an ordinary Go expression over the model's fields.
    v.When(in.Role == "admin" && in.Age < 21).
        Reject(&in.Role, "an admin must be at least 21")
}
```

```go [handlers/users.go]
// CreateUser registers a user.
//
// By the time this runs the model has been validated, so the handler can take
// the input at face value: the email is trimmed and lower-cased, the password
// is long enough, and the role is one of the three it is allowed to be.
func CreateUser(ctx *muzak.Context, in schemas.CreateUserIn) (schemas.UserOut, error) {
    return schemas.UserOut{Username: in.Username, Email: in.Email}, nil
}
```

::

## Where to go next

[Error Handling](/docs/getting-started/error-handling) covers the envelope these
failures arrive in, and [OpenAPI](/docs/fundamentals/openapi) covers the document
the constraints reach.


--------------------------------------------------------------------------------
title: "API Versioning"
description: "Serve several versions of an API from one application, with the version read from the path, a header, the Accept header or a function of your own."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/fundamentals/versioning"
--------------------------------------------------------------------------------

# API Versioning

Versioning is off until `AppOptions.Versioning` names a type. Once it is on, a route says
which version or versions it answers, and a route that says nothing answers nothing.

```go [cmd/main.go]
app := muzak.New(muzak.AppOptions{
    Title:      "Awesome API",
    Version:    "1.0.0",
    Versioning: muzak.VersioningOptions{Type: muzak.VersioningURI},
})

app.Get("/cats", handlers.ListCatsV1, muzak.WithVersion("1"))
app.Get("/cats", handlers.ListCatsV2, muzak.WithVersion("2"))
app.Get("/health", handlers.Health, muzak.WithVersion(muzak.VersionNeutral))
```

```bash
curl http://localhost:8080/v1/cats   # ListCatsV1
curl http://localhost:8080/v2/cats   # ListCatsV2
curl http://localhost:8080/health    # Health, at its plain path
curl http://localhost:8080/v3/cats   # 404
```

`AppOptions.Version` and `AppOptions.Versioning` are different things. The first is the
API's own version string in the OpenAPI document; the second is how a request says which
version of the API it wants.

## Choosing a type

```go
muzak.VersioningOptions{Type: muzak.VersioningURI}
```

| Type | The version comes from | Needs |
|---|---|---|
| `VersioningNone` | nowhere. Versioning is off, and `WithVersion` may not be used anywhere | |
| `VersioningURI` | the request path, such as `/v1/cats` | |
| `VersioningHeader` | a request header | `Header` |
| `VersioningMediaType` | a parameter of the `Accept` header, such as `application/json;v=2` | `Key` |
| `VersioningCustom` | a function of your own | `Extractor` |

`VersioningURI` is the one a new application should reach for first. It is the only type
where the version is part of routing rather than something read off a request, which is
what makes it visible in a URL, cacheable by anything in front of the service, and
representable in the generated document.

## Declaring what a route answers

`WithVersion` is a shared option, so it works on a route, on a router, and at the point one
router is included into another.

```go [routers/cats.go]
func Cats() *muzak.Router {
    // Every route below answers version 1 unless it says otherwise.
    r := muzak.NewRouter(muzak.WithTags("cats"), muzak.WithVersion("1"))

    r.Get("/cats", handlers.ListCats)
    r.Get("/cats/{id}", handlers.ReadCat)

    // This one is only in version 2.
    r.Post("/cats/{id}/adopt", handlers.AdoptCat, muzak.WithVersion("2"))

    return r
}
```

A route's own declaration **replaces** what it inherited rather than adding to it, exactly
as `Status` replaces a router's declared default. `AdoptCat` above answers version 2 and
not version 1.

### Several versions at once

```go
// One handler, two versions. Nothing changed between them.
r.Get("/cats", handlers.ListCats, muzak.WithVersion("1", "2"))
```

Calling `WithVersion` with no arguments at all is a build error, since there would be
nothing left for the declaration to mean.

### Version-neutral routes

```go
r.Get("/health", handlers.Health, muzak.WithVersion(muzak.VersionNeutral))
```

`VersionNeutral` answers every request regardless of the version it names, including a
request that names none. Under `VersioningURI` a neutral route keeps its plain path, with
no version segment inserted, so the route above is reached at `/health` and **not** at
`/v1/health`.

It cannot be combined with another version in the same call, because it already answers
every request the narrower version would.

## The strict default

A route that resolves no version at all answers no request while versioning is enabled. It
is not a build error, and it is not served unversioned either: an application has to opt a
route into being version-independent deliberately, with `VersionNeutral`, rather than by
forgetting.

The build says so rather than leaving you to find out:

```
14:32:07.488 WARN  [Router]        GET /cats declares no version and no default version applies; it will answer no request while versioning is enabled
```

Where most routes share one version, say so once instead of on every route:

```go
muzak.VersioningOptions{
    Type:           muzak.VersioningURI,
    DefaultVersion: []muzak.Version{"1"},
}
```

`DefaultVersion` applies to any route or router that declares none of its own. Setting it
while `Type` is still `VersioningNone` is a build error, because it could not mean
anything.

## URI versioning

The version is inserted into the path of every route that declares one.

```go
muzak.VersioningOptions{Type: muzak.VersioningURI}
```

| Declaration | Path |
|---|---|
| `r.Get("/cats", h, muzak.WithVersion("1"))` | `/v1/cats` |
| `r.Get("/cats", h, muzak.WithVersion("1", "2"))` | `/v1/cats` and `/v2/cats` |
| `r.Get("/health", h, muzak.WithVersion(muzak.VersionNeutral))` | `/health` |

A route naming more than one version is registered once per version, each at its own path,
because here the version is part of routing rather than something matched once a request
arrives.

### The prefix

`v` by default, so version `1` is reached at `/v1`. `Prefix` is a `*string`, which is what
lets an empty prefix be told from an unset one.

```go
prefix := "ver-"
muzak.VersioningOptions{Type: muzak.VersioningURI, Prefix: &prefix}
// version "1" is reached at /ver-1/cats
```

```go
none := ""
muzak.VersioningOptions{Type: muzak.VersioningURI, Prefix: &none}
// version "1" is reached at /1/cats
```

### Versions come before router prefixes

The version segment is prepended to the route's **fully resolved** path, after every
`WithPrefix` has already been applied.

```go
r := muzak.NewRouter(muzak.WithVersion("1"))
r.Get("/cats", handlers.ListCats)

app.Include(r, muzak.WithPrefix("/api"))
```

```
/v1/api/cats
```

Not `/api/v1/cats`. If you want the version inside a prefix, put the prefix in the route
paths themselves and leave the include unprefixed, or use one of the header-based types
where the path is left alone entirely.

## Header versioning

```go
muzak.VersioningOptions{
    Type:   muzak.VersioningHeader,
    Header: "X-API-Version",
}
```

```go
app.Get("/cats", handlers.ListCatsV1, muzak.WithVersion("1"))
app.Get("/cats", handlers.ListCatsV2, muzak.WithVersion("2"))
```

```bash
curl http://localhost:8080/cats -H 'X-API-Version: 1'   # ListCatsV1
curl http://localhost:8080/cats -H 'X-API-Version: 2'   # ListCatsV2
curl http://localhost:8080/cats -H 'X-API-Version: 3'   # 404
curl http://localhost:8080/cats                         # 404, no version named
```

The path is left alone, so more than one route may share it, as long as their versions
never overlap. Two routes at one method and path that both answer version `1` are reported
as registered twice when the application is built.

A request naming no version at all is answered only by a route registered `VersionNeutral`.

## Media type versioning

```go
muzak.VersioningOptions{
    Type: muzak.VersioningMediaType,
    Key:  "v=",
}
```

```bash
curl http://localhost:8080/cats -H 'Accept: application/json;v=2'
```

`Key` is the literal prefix to look for among the parameters of the `Accept` header,
trailing `=` included. The header may name several media ranges separated by commas, each
with its own parameters separated by semicolons, and the first parameter carrying the key
wins.

An `Accept` header with no such parameter names no version, so only a `VersionNeutral`
route answers it.

## Custom versioning

```go
muzak.VersioningOptions{
    Type:      muzak.VersioningCustom,
    Extractor: core.VersionsFromHeader,
}
```

```go [core/versioning.go]
// VersionsFromHeader reads a comma-separated list of acceptable versions,
// most preferred first, so a client can say "2 if you have it, otherwise 1".
func VersionsFromHeader(r *http.Request) []string {
    raw := r.Header.Get("X-Versions")
    if raw == "" {
        return nil
    }
    return strings.Split(raw, ",")
}
```

```bash
curl http://localhost:8080/cats -H 'X-Versions: 3,2,1'   # answered by version 2
```

The extractor returns versions in order from most to least preferred, and the first one
some route actually answers wins. Empty strings are dropped, and returning nil or an empty
slice reports a request that named no version at all.

`VersioningCustom` is the only type that can offer several versions in one request. It is
also where a version that is not a plain string belongs: a date, a build number, or a value
read from a query parameter or a cookie.

## How a request is matched

1. The path and method are resolved as they always are. For `VersioningURI` that has
   already settled the version, since it is part of the path.
2. For every other type, the version or versions the request declares are extracted.
3. Each requested version is tried in order, from most to least preferred, against every
   route registered for that method and path.
4. If nothing matched, a route registered `VersionNeutral` answers.
5. If nothing answers, the request gets the same `404` a path that matches nothing gets.

```json
{
  "error": {
    "code": "not_found",
    "message": "no route matches GET /cats",
    "status": 404
  },
  "request_id": "0611f4b2-2f0a-4b57-9c1a-6e6a2e2f9b31"
}
```

A version nobody serves is a `404` rather than a `406`, because as far as routing is
concerned the request named something that does not exist.

An application that never enables versioning pays nothing for any of this: there is exactly
one route per method and path, and it is dispatched without a version ever being resolved.

## Errors reported when the application is built

| Problem | Reported as |
|---|---|
| `WithVersion` used while `Type` is `VersioningNone` | a version is declared but versioning is not enabled |
| `WithVersion()` with no versions | `WithVersion` was called with no versions |
| `VersionNeutral` combined with another version | `VersionNeutral` cannot be combined with another version |
| Two routes at one method and path with overlapping versions | is registered twice |
| `DefaultVersion` set while `Type` is `VersioningNone` | `DefaultVersion` is set but `Type` is not |
| `VersioningHeader` with no `Header` | `Header` must name a header for header versioning |
| `VersioningMediaType` with no `Key` | `Key` must be set for media type versioning |
| `VersioningCustom` with no `Extractor` | `Extractor` must be set for custom versioning |

All of them arrive together, with everything else `Build` reports.

## Versioning and the generated document

**Operation identifiers stay unique**, which is what a client generator needs.

| Scheme | Declaration | Identifier |
|---|---|---|
| URI | `r.Get("/cats", h, WithVersion("1"))` | `get_v1_cats`, derived from the versioned path |
| URI | with `OperationID("listCats")` and versions `1`, `2` | `listCats_1` and `listCats_2` |
| Header, media type, custom | `r.Get("/cats", h, WithVersion("1"))` | `get_cats_1` |
| Any | `WithVersion(muzak.VersionNeutral)` | no suffix |

**URI versioning describes every version.** Each one is a distinct path, so `/v1/cats` and
`/v2/cats` are two entries in the document and both appear in the documentation UI.

**The other schemes describe one.** Several routes then share one path and method, and an
OpenAPI path item has a single slot per method, so the last registration wins and the
earlier versions are absent from the document. Nothing about routing changes; only the
description does.

That is the practical argument for `VersioningURI` whenever the generated document
matters. Where a header-based scheme is required anyway, the options are to keep the
document to the current version and describe the older ones elsewhere, or to generate one
document per version by building an application per version:

```go [cmd/openapi/main.go]
for _, version := range []muzak.Version{"1", "2"} {
    app := buildApp(version)   // includes only the routers for that version
    document, err := app.Document()
    // ... write it to openapi-v{version}.json
}
```

## Inspecting what was registered

`Route.Versions` holds the versions a route resolved to, from `WithVersion` or from
`DefaultVersion`. It is empty for an application that never enables versioning, and also,
deliberately, for a route that answers nothing because neither it nor anything it was
declared under named a version.

The fields are filled in when the application is built, so read them after `Build`.

```go
cats := routers.CatsV1()
app.Include(cats)

if err := app.Build(); err != nil {
    log.Fatal(err)
}
for _, route := range cats.Routes() {
    log.Printf("%s %s versions=%v", route.Method, route.Path, route.Versions)
}
```

```
GET /cats versions=[1]
GET /cats/{id} versions=[1]
```

`Routes()` reports the routes registered directly on the router it is called on, excluding
those of any router included into it, so hold on to the routers you want to inspect rather
than asking the application for everything.

Two details are worth knowing when reading those paths. They carry every inherited prefix
once the application is built, and under `VersioningURI` they are the paths *before* the
version segment is inserted: the versioned clones the router actually dispatches are the
framework's, not the ones this list holds.

## A version per package

Once two versions differ in more than a handler, give each its own router and let `main`
mount both. The schemas stay separate, which is the point: a version exists so that the
old shape can keep working while the new one changes.

::code-group

```go [routers/cats_v1.go]
func CatsV1() *muzak.Router {
    r := muzak.NewRouter(muzak.WithTags("cats"), muzak.WithVersion("1"), muzak.Deprecated())

    r.Get("/cats", handlers.ListCatsV1, muzak.Summary("List cats"))
    r.Get("/cats/{id}", handlers.ReadCatV1, muzak.Summary("Read a cat"))

    return r
}
```

```go [routers/cats_v2.go]
func CatsV2() *muzak.Router {
    r := muzak.NewRouter(muzak.WithTags("cats"), muzak.WithVersion("2"))

    r.Get("/cats", handlers.ListCatsV2, muzak.Summary("List cats"))
    r.Get("/cats/{id}", handlers.ReadCatV2, muzak.Summary("Read a cat"))
    r.Post("/cats/{id}/adopt", handlers.AdoptCat, muzak.Summary("Adopt a cat"))

    return r
}
```

```go [cmd/main.go]
app := muzak.New(muzak.AppOptions{
    Title:      settings.AppName,
    Version:    "2.0.0",
    Addr:       settings.Addr,
    Versioning: muzak.VersioningOptions{Type: muzak.VersioningURI},
})

app.Include(routers.CatsV1())
app.Include(routers.CatsV2())

// Operational routes belong to no version.
app.Include(routers.Meta(), muzak.WithVersion(muzak.VersionNeutral))
```

::

`Deprecated()` on the version 1 router marks every operation beneath it as deprecated in
the document and changes nothing at runtime, which is the honest way to announce a removal
before making it.

Retiring a version is then deleting one `Include` and one package.

## Testing versioned routes

```go [handlers/cats_test.go]
func TestCatsByVersion(t *testing.T) {
    client := testclient.New(t, buildApp())

    client.Get("/v1/cats").
        AssertStatus(http.StatusOK).
        AssertJSON(`{"cats":[{"name":"Plumbus"}]}`)

    client.Get("/v2/cats").
        AssertStatus(http.StatusOK).
        AssertJSON(`{"cats":[{"name":"Plumbus","adopted":false}]}`)

    // A version nobody serves is a 404, the same as any unmatched path.
    client.Get("/v3/cats").AssertStatus(http.StatusNotFound)

    // A version-neutral route keeps its plain path under URI versioning.
    client.Get("/healthz").AssertStatus(http.StatusOK)
}
```

```go
func TestCatsByHeader(t *testing.T) {
    client := testclient.New(t, buildHeaderVersionedApp())

    client.Get("/cats", testclient.Header("X-API-Version", "1")).AssertStatus(http.StatusOK)
    client.Get("/cats", testclient.Header("X-API-Version", "9")).AssertStatus(http.StatusNotFound)

    // No version named at all, and no neutral route to fall back to.
    client.Get("/cats").AssertStatus(http.StatusNotFound)
}
```

A test that every route answers something is worth more than usual here, because a route
that answers nothing is a log line rather than a build error:

```go
func TestEveryRouteAnswersSomething(t *testing.T) {
    app := muzak.New(appOptions())

    // Hold on to each router, because Routes() reports only what was
    // registered directly on the router it is called on.
    all := []*muzak.Router{routers.CatsV1(), routers.CatsV2(), routers.Meta()}
    app.Include(all[0])
    app.Include(all[1])
    app.Include(all[2], muzak.WithVersion(muzak.VersionNeutral))

    if err := app.Build(); err != nil {
        t.Fatalf("Build = %v", err)
    }
    for _, r := range all {
        for _, route := range r.Routes() {
            if len(route.Versions) == 0 {
                t.Errorf("%s %s answers no version", route.Method, route.Path)
            }
        }
    }
}
```

## Where to go next

[Routers](/docs/getting-started/routers) covers the prefixes and options a
version declaration layers on top of, and [OpenAPI](/docs/fundamentals/openapi)
covers the document each version produces.


--------------------------------------------------------------------------------
title: "Logging"
description: "Structured logging that is readable in development and parseable in production, with scopes, request identifiers and secrets redacted by key."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/fundamentals/logging"
--------------------------------------------------------------------------------

# Logging

Muzak logs through `log/slog`. The handler it installs picks its format by looking at
where its output is going: the aligned console format on a terminal, one JSON object per
record anywhere else.

```
14:32:07.482 INFO  [Server]        Starting Muzak application...
14:32:07.492 INFO  [Server]        Listening on :8080
14:32:10.114 INFO  [Request]       GET /items/  status=200  duration=412µs  bytes=181  request_id=0611f4b2
14:32:10.115 INFO  [UsersService]  user created  user_id=42  request_id=0611f4b2
```

Colour is used only when the output is a terminal and `NO_COLOR` is unset.

## Reaching the logger

```go
// The application's logger.
log := app.Logger()
```

```go
// Inside a handler, the same logger annotated by the logging middleware with
// per-request attributes such as the method, path and request identifier.
func ListItems(ctx *muzak.Context, _ muzak.Empty) (schemas.ItemListOut, error) {
    ctx.Logger().Info("listing items")
    // ...
}
```

## Scopes

Give each subsystem its own scope so its lines line up in the console and can be filtered
in production.

```go
log := muzak.Scoped(app.Logger(), "UsersService")
log.Info("user created", "user_id", 42)
```

```
14:32:10.115 INFO  [UsersService]  user created  user_id=42
```

The scope is an ordinary attribute under `muzak.ScopeKey`, which the console handler
lifts out of the attribute list and renders as the bracketed column after the level.
`Scoped` returns the logger unchanged when it is nil, so it is safe to call on an
application that has not been built yet.

The framework uses four scopes of its own, so its lines can be told apart from yours at a
glance:

| Constant | Value | Covers |
|---|---|---|
| `ScopeServer` | `Server` | Start-up, listening, shutdown, panics |
| `ScopeRouter` | `Router` | Route registration and the routing table |
| `ScopeRequest` | `Request` | The per-request access log |
| `ScopeDocs` | `Docs` | The OpenAPI document and the documentation UI |

## Configuring it

```go
app := muzak.New(muzak.AppOptions{
    Title: "Awesome API",
    LoggerOptions: muzak.LoggerOptions{
        Level:  slog.LevelDebug,
        Format: muzak.LogFormatConsole,
    },
})
```

| Field | Default | What it does |
|---|---|---|
| `Level` | `slog.LevelInfo` | The minimum level emitted. Pass a `*slog.LevelVar` to change it while the process runs |
| `Format` | `LogFormatAuto` | `Auto`, `Console`, `JSON` or `None` |
| `Output` | `os.Stderr` | Where records are written, which keeps logs out of a program's data output |
| `Color` | terminal and `NO_COLOR` | Forces ANSI colour on or off for the console format |
| `TimeFormat` | `15:04:05.000` | The timestamp layout in the console format |
| `ScopeWidth` | `16` | The column reserved for the bracketed scope. A longer scope pushes the message right rather than being truncated |
| `AddSource` | off | Records the source file and line. It costs a stack walk per record |
| `RedactKeys` | `DefaultRedactedKeys` | Replaces the redaction list |
| `ShortRequestID` | on | Truncates request identifiers to eight characters in the console format only |

### Formats

| Value | Behaviour |
|---|---|
| `LogFormatAuto` | Console when the output is an interactive terminal, JSON otherwise |
| `LogFormatConsole` | Always the aligned, optionally coloured console format |
| `LogFormatJSON` | Always one JSON object per record |
| `LogFormatNone` | Discards every record. The fastest option, and what tests use to keep output clean |

`ShortRequestID` never applies to JSON output, where the full identifier is always
written.

#### Auto, in a container

A container's stdout is not a terminal, so a service run under Docker or Compose gets JSON
even on a laptop. That is the right default for a production container and a surprise in
front of `docker compose logs`. Name the format to read it there, and ask for colour
separately, since `Color` follows its own terminal check:

```yaml [docker-compose.yml]
environment:
  # Both, or the output is JSON with no colour. Production sets json.
  LOG_FORMAT: console
  LOG_COLOR: "true"
```

```go [core/logging.go]
muzak.LoggerOptions{
    Format: muzak.LogFormatConsole,
    Color:  &colour, // *bool, so "unset" and "off" stay different things
}
```

### Changing the level at runtime

```go
var level slog.LevelVar

app := muzak.New(muzak.AppOptions{
    Title:         "Awesome API",
    LoggerOptions: muzak.LoggerOptions{Level: &level},
})

// Later, from a handler or a signal:
level.Set(slog.LevelDebug)
```

## Bringing your own logger

```go
app := muzak.New(muzak.AppOptions{
    Title:  "Awesome API",
    Logger: slog.New(myHandler),
})
```

`AppOptions.Logger` takes precedence, and `LoggerOptions` is then unused. To build
Muzak's own handler outside an application, call `muzak.NewLogger`:

```go
log := muzak.NewLogger(muzak.LoggerOptions{Format: muzak.LogFormatJSON})
```

The result is safe for concurrent use, redacts sensitive attributes, and performs no
formatting work for records below its level.

## Redaction

Credentials reach logs by accident far more often than by design, most often through an
attribute carrying a whole header map or request struct. Attribute values under these keys
are replaced with `muzak.RedactedPlaceholder`, which is `[redacted]`:

```
authorization        proxy-authorization  cookie          set-cookie
password             passwd               secret          token
access-token         refresh-token        api-key         apikey
private-key          client-secret        session         credentials
```

```go
log.Info("calling upstream", "api_key", key)
```

```
14:32:10.115 INFO  [Upstream]      calling upstream  api_key=[redacted]
```

Matching is case-insensitive and ignores `-` and `_`, so `API-Key`, `api_key` and `apikey`
are all caught.

Replace the list with one of your own:

```go
muzak.LoggerOptions{
    RedactKeys: append(muzak.DefaultRedactedKeys, "internal_user_id"),
}
```

Passing an empty, non-nil slice disables redaction entirely, which is only appropriate
when nothing sensitive can reach the logger.

Redaction covers attribute values, not message text. `log.Info("token is " + token)` is
still a leak, and no key-based rule can catch it.

## The access log

One line per request, written by the middleware Muzak installs by default.

```
14:32:10.114 INFO  [Request]       GET /items/  status=200  duration=412µs  bytes=181  request_id=0611f4b2
```

```go
app := muzak.New(muzak.AppOptions{
    Title: "Awesome API",
    AccessLogOptions: muzak.AccessLogOptions{
        Level:     slog.LevelInfo,
        SkipPaths: []string{"/healthz", "/metrics"},
    },
})
```

Successful responses use `Level`. Server errors are always logged at error level and
client errors at warn level, so a quiet production level still surfaces failures.

Only fixed, non-sensitive fields are recorded. Query strings, request bodies and headers
are deliberately omitted, because each of them routinely carries credentials or personal
data that should not be duplicated into a log store.

`DisableAccessLog` removes the middleware entirely.

## Request identifiers

Every request is assigned one, echoed in the `X-Request-Id` response header, copied into
the `request_id` member of an error body, and recorded under `muzak.RequestIDKey` on
every log line the request produces.

```go
// Inside a handler.
ctx.RequestID()
```

```go
// In code that has only a context.Context, such as a repository or a client
// wrapper that wants to propagate the identifier downstream.
id, ok := muzak.RequestIDFromContext(ctx)
```

The console handler shortens values under that key to their first eight characters, which
keeps a development session narrow while JSON output keeps the identifier in full. Turn it
off with `ShortRequestID`.

Identifiers are UUID version 7, which sort chronologically, so a log store's index on the
field is a time index for free.

## Logging from a goroutine that outlives the request

A `Context` is pooled and reused, so it must not be captured. Pass `ctx.Context()` instead
and copy out whatever else you need first.

```go
func Enqueue(ctx *muzak.Context, in JobIn) (JobOut, error) {
    log := muzak.Scoped(ctx.Logger(), "Jobs")
    requestCtx := ctx.Context()
    id := in.ID

    go func() {
        if err := process(requestCtx, id); err != nil {
            log.Error("job failed", "job_id", id, "error", err.Error())
        }
    }()

    return JobOut{ID: id}, nil
}
```

## Quiet logs in tests

```go
app := muzak.New(muzak.AppOptions{
    Title:         "Awesome API",
    LoggerOptions: muzak.LoggerOptions{Format: muzak.LogFormatNone},
})
```

## Where to go next

[Error Handling](/docs/getting-started/error-handling) covers what reaches the
log when a request fails, and [Testing](/docs/fundamentals/testing) covers
exercising an application in-process.


--------------------------------------------------------------------------------
title: "OpenAPI"
description: "An OpenAPI 3.1 document and a self-contained documentation page, derived from the routes, the types and the validation rules you already wrote."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/fundamentals/openapi"
--------------------------------------------------------------------------------

# OpenAPI

Every Muzak application describes itself. The OpenAPI 3.1 document is served at
`/openapi.json` and a documentation page at `/docs`, both derived from the registrations
themselves: path templates, tags, summaries, the schemas of the `In` and `Out` types, the
declared statuses, the validation constraints and the entries added by `WithResponseDoc`
and `WithResponseModel`.

All of that reflection happens once, while the application is being built. Nothing on the
request path inspects a type.

## Describing the API

`AppOptions` embeds `OpenAPIOptions`, so its fields are set inline.

```go
app := muzak.New(muzak.AppOptions{
    Title:       "Awesome API",
    Version:     "1.0.0",
    Description: "The example service, rebuilt on Muzak.",
    TermsOfService: "https://example.com/terms",
    Contact: &muzak.Contact{
        Name:  "API team",
        Email: "api@example.com",
        URL:   "https://example.com/support",
    },
    License: &muzak.License{
        Name:       "Apache 2.0",
        Identifier: "Apache-2.0",
    },
    Servers: []muzak.Server{
        {URL: "https://api.example.com", Description: "production"},
        {URL: "http://localhost:8080", Description: "development"},
    },
    Addr: ":8080",
})
```

`Title` defaults to `Muzak API` and `Version` to `0.1.0`. `Description` is rendered as
CommonMark by documentation tools. In OpenAPI 3.1 a licence carries either an `Identifier`
or a `URL`, not both. With no `Servers` at all, tools treat the document's own origin as
the server.

## Where it is served

| Option | Default | Effect |
|---|---|---|
| `DocsUI` | nil | The page to serve. With none, the document is published and no page is |
| `DocsPath` | `/docs` | Where that page is served |
| `OpenAPIPath` | `/openapi.json` | Where the document is served |
| `DisableDocs` | off | Serves neither, for a deployment that must not describe itself |

```go
app := muzak.New(muzak.AppOptions{
    Title:       "Awesome API",
    DocsUI:      ui.Files(),
    DocsPath:    "/reference",
    OpenAPIPath: "/reference/openapi.json",
})
```

Both paths are absolute and matched exactly. Where they ended up is the last thing the
server says as it starts, as a URL you can open:

```
INFO [Server]  Listening on [::]:8080  scheme=http
INFO [Docs]    Documentation at http://localhost:8080/reference  openapi=http://localhost:8080/reference/openapi.json
```

A socket bound to every interface is reported as `localhost`, because `[::]` is where the
process listens rather than somewhere a browser can go. With no `DocsUI`, the line names
the document alone and says how to add a page; with `DisableDocs` set, nothing is
announced and neither path is registered.

Three ways of configuring a path that could not work are build errors rather than a page
nobody can reach:

```
muzak: AppOptions.DocsPath is "reference", which is not an absolute path and so would answer no request; write it as "/reference"
muzak: AppOptions.DocsPath and AppOptions.OpenAPIPath are both "/docs", but the page and the document it reads need an address each
muzak: AppOptions.DocsPath is "/docs", which a route of this application already answers; move one of the two, or set AppOptions.DisableDocs to serve no documentation at all
```

That last one matters: the documentation is matched before the router, so without the
check a route of your own at `/docs` would be shadowed silently. `DocsPath` is only
checked when a page is actually served there, so an application that configures no
`DocsUI` may use it for a route of its own, and one that sets `DisableDocs` may use both
paths.

## The documentation dashboard

Generating the document is the framework's job. Rendering it is not, and a service that
wants no page should not carry one, so the dashboard is a module of its own:

```bash
go get muzak.dev/openapi
```

```go
import (
    "muzak.dev/framework"
    "muzak.dev/openapi/ui"
)

app := muzak.New(muzak.AppOptions{
    Title:  "Awesome API",
    DocsUI: ui.Files(),
})
```

Go downloads and links a module only when something imports it, so leaving `DocsUI` unset
costs a binary nothing at all rather than embedding a page it will never serve. Adding it
costs about two megabytes, and they are in the binary rather than fetched from anywhere:
nothing is downloaded at run time, in either direction.

The page fetches no script, stylesheet, font or image from a third party, and the only
requests it makes are for this application's own document and, from the console, this
application's own routes. That is what lets it run under a content security policy that
hashes its own inline script and allows no network access beyond this origin. An
air-gapped deployment gets working documentation with no further setup.

What it gives a reader:

- Operations grouped by category, one per endpoint, with a **Tags** index beneath for the
  labels that cut across categories, and a filter over both.
- Each schema as an outline that expands, carrying the constraints the application
  actually enforces, beside a generated example.
- A **console** on every operation: fill in the parameters, the JSON body or the multipart
  form, send the request from the page, and read the status, timing, size, headers and
  body. An event stream is read as it arrives and stopped when you have seen enough.
- Request snippets in nine languages, and **copy as curl** for the requests the page
  cannot make itself, such as one to another origin.
- **Authorize**, which adds a bearer token, an API key header or basic credentials to what
  the console sends. They are held in the tab, never stored, and forgotten when the page
  is closed.
- A light, dark or system theme.

The page, its assets and the document are rendered, hashed and compressed once while the
application is built, so a request for any of them is a header write and a copy of bytes
that never change. Each carries an `ETag`, so a reader who reloads transfers nothing, and
a client that accepts gzip gets a fraction of the bytes.

### Serving a dashboard of your own

`DocsUI` takes any `fs.FS`, so the dashboard above is one implementation of a small
contract rather than the only choice:

- an `index.html` at the root of the file system;
- every absolute URL in it written under `/__muzak_docs__/`, which is rewritten to
  `DocsPath` when the application is built;
- the OpenAPI document fetched from `/__muzak_spec__`, rewritten to `OpenAPIPath`.

No other file may carry an absolute URL, because only the page is rewritten. Assets are
served beneath `DocsPath`, so the whole tree moves with it.

## Grouping operations

Tags decide the groups the reference is presented in. A router's tags are inherited by
every route beneath it:

```go
r := muzak.NewRouter(muzak.WithTags("items"))
```

```go
app.Include(routers.Admin(),
    muzak.WithPrefix("/admin"),
    muzak.WithTags("admin"))
```

A route can name a tag of its own, which **adds to** what it inherited rather than
replacing it, so the operation is documented under every group it names:

```go
r.Post("/", handlers.AdminAction,
    muzak.Status(http.StatusCreated),
    muzak.WithTags("audit"),
    muzak.Summary("Admin action"))
```

```json
"tags": ["admin", "audit"]
```

Duplicates are removed while first-seen order is preserved.

### Describing a group

Naming a tag is enough to group by it, but a group with no explanation and no place in the
running order is a heading and nothing more. `OpenAPIOptions.Tags` gives it both:

```go
app := muzak.New(muzak.AppOptions{
    Title: "Awesome API",
    Tags: []muzak.Tag{
        {Name: "items", Description: "Everything the catalogue holds."},
        {Name: "admin", Description: "Operations that need a staff token."},
        {Name: "audit", Description: "Anything that leaves a trace."},
    },
})
```

Described tags lead, in the order they are declared. A tag only a route names follows, in
the order the routes named it. A tag described here that no route carries is left out
rather than rendering as an empty group, so deleting the last route in a group also
removes the group.

## Documenting one operation

```go
r.Post("/items/", handlers.CreateItem,
    muzak.Summary("Create an item"),
    muzak.Description("Creates an item under the identifier the caller chooses."),
    muzak.Status(http.StatusCreated),
    muzak.WithResponseDoc(http.StatusConflict, "An item with that identifier already exists"))
```

`Summary` is the one-line description shown beside the operation. `Description` is the
long-form one, carried verbatim and rendered as CommonMark.

### Operation identifiers

Client generators use the operation identifier to name the method they emit. Without one,
Muzak derives it from the method and path: `GET /items/{item_id}` becomes
`get_items_by_item_id`.

```go
r.Get("/items/{item_id}", handlers.ReadItem, muzak.OperationID("readItem"))
```

Identifiers must be unique across the application. A collision is reported when the
application is built.

With versioning enabled, a version is folded into the identifier so that two versions of
one operation stay distinct: `get_v1_cats` under URI versioning, `get_cats_1` under the
header-based schemes, and `listCats_1` where an explicit `OperationID("listCats")` was
given. See [Versioning](/docs/fundamentals/versioning).

### Hiding and deprecating

```go
r.Get("/healthz", handlers.Health, muzak.Hidden(), muzak.SkipRateLimit())
```

`Hidden` leaves a route fully routable but out of the document and the documentation page,
which is what a health check or an internal endpoint wants. `Deprecated` marks an
operation as no longer recommended and changes nothing at runtime. Both are shared
options, so a whole router can carry either.

## What the schemas are built from

### Parameters

A field's location tag becomes the parameter's `in`, its `doc` tag becomes the
description, its `default` tag becomes the default, and its requiredness follows the rules
in [Request Data](/docs/getting-started/request-data).

```go
type UserListQuery struct {
    Limit  int    `query:"limit" default:"20" doc:"Maximum number of users to return"`
    Cursor string `query:"cursor" doc:"Opaque cursor from a previous page"`
}
```

```json
{
  "name": "limit",
  "in": "query",
  "description": "Maximum number of users to return",
  "schema": { "type": "integer", "format": "int64", "default": "20", "minimum": 1, "maximum": 100 }
}
```

The `minimum` and `maximum` above were never written twice. They come from
`v.Number(&in.Limit).Between(1, 100)` in the model's `Validate` method, which is why the
document cannot drift from what the code enforces. The full mapping is in
[Validation](/docs/fundamentals/validation).

That guarantee is the reason numeric rules changed in 0.2.7. A bound that excludes zero
is now applied to zero, because until then `minimum: 1` was published while the server
accepted `0` -- the one case where this document and the code did drift, and the reader
who trusted the document was the one who got it wrong.

### Request bodies

A named struct becomes a component and is referenced by name, so a type used by several
operations is described once.

```json
"requestBody": {
  "required": true,
  "content": {
    "application/json": {
      "schema": { "$ref": "#/components/schemas/ItemCreateIn" }
    }
  }
}
```

A member is required unless it is a pointer, carries `omitzero` or `omitempty`, carries a
`default`, or is tagged `required:"false"`. A pointer member is widened so that `null` is
permitted.

Two types with the same name in different packages are told apart by qualifying the second
with its package, so `schemas.ItemOut` and `core.ItemOut` do not collide.

### Well-known types

| Go type | Schema |
|---|---|
| `time.Time` | `{"type": "string", "format": "date-time"}` |
| `uuid.UUID` | `{"type": "string", "format": "uuid"}` |
| a type implementing both `encoding.TextMarshaler` and `encoding.TextUnmarshaler` | a string |
| a map | an object with `additionalProperties` |
| an interface | an empty schema, which accepts any JSON value |

A type that converts to and from text is a string everywhere it appears: in a parameter,
where the binder parses it with `UnmarshalText`, and in a body, where the encoder writes it
with `MarshalText`. Describing it by walking its fields would document the Go struct rather
than the value on the wire.

### Responses

| Route kind | What is documented |
|---|---|
| Ordinary | The route's declared status, carrying the `Out` schema as `application/json` |
| `Out` is `muzak.HTML` | The same status, carrying `text/html` |
| `Out` is `muzak.Empty` | The same status, with no content |
| `Router.SSE` | `200` carrying `text/event-stream`, whose schema describes one event's data |
| `Router.WS` | `101 Switching Protocols`, because the conversation continues off the document |

Alongside that, every route that binds anything documents `422` with the error envelope,
every declared outcome is added, and a `default` response carrying the error envelope is
added unless one was declared.

A declared outcome is either `WithResponseDoc(code, description)`, described as the error
envelope because that is what a returned error produces, or
`WithResponseModel[T](code, description)`, described as `T` exactly as an `Out` type would
be. The last declaration of a status code wins, so a route replaces what it inherited from
its router, and an empty description falls back to the code's standard reason phrase. See
[Response Models](/docs/techniques/response-models).

### Multipart bodies

A route binding `form` or `file` fields is described as `multipart/form-data`. A route
that binds form values and no files at all is also described as
`application/x-www-form-urlencoded`, because that is what a plain HTML form posts.

### Versioned routes

Under `VersioningURI` each version is a distinct path, so `/v1/cats` and `/v2/cats` are two
entries and every version is described.

Under `VersioningHeader`, `VersioningMediaType` and `VersioningCustom` several routes share
one path and method, and an OpenAPI path item has a single slot per method. The last
registration wins, so only one version of that operation reaches the document. Routing is
unaffected; only the description is. That is the practical argument for URI versioning
whenever the generated document matters, and
[Versioning](/docs/fundamentals/versioning) covers the alternative of generating
one document per version.

## Reading the document from Go

```go
document, err := app.Document()
if err != nil {
    return err
}
for path, item := range document.Paths {
    if item.Get != nil {
        fmt.Println("GET", path, item.Get.OperationID)
    }
}
```

`Document` builds the application if necessary. It returns nil when documentation is
disabled with `DisableDocs`, and an error when the application does not build.

## Committing the document

`Document.Marshal` renders indented JSON with map keys in sorted order, so the output is
byte-for-byte reproducible. That is what lets a generated document be committed and
diffed, and a diff in a pull request is how an accidental breaking change gets noticed.

```go [cmd/openapi/main.go]
// Command openapi writes the generated OpenAPI document to standard output.
package main

import (
    "log"
    "os"

    "awesome-api/routers"
    "muzak.dev/framework"
)

func main() {
    app := muzak.New(muzak.AppOptions{Title: "Awesome API", Version: "1.0.0"})
    app.Include(routers.Users())
    app.Include(routers.Items())

    document, err := app.Document()
    if err != nil {
        log.Fatal(err)
    }
    out, err := document.Marshal()
    if err != nil {
        log.Fatal(err)
    }
    if _, err := os.Stdout.Write(out); err != nil {
        log.Fatal(err)
    }
}
```

A composition shared between `cmd/main.go` and this command is worth factoring into a
function of its own, so the document is generated from exactly the application that runs.

```bash
go run ./cmd/openapi > openapi.json
git diff --exit-code openapi.json
```

The same document is what a client generator reads:

```bash
curl -s http://localhost:8080/openapi.json > openapi.json
```

## Where to go next

[Validation](/docs/fundamentals/validation) covers the constraints that reach the
schemas, and [Testing](/docs/fundamentals/testing) covers asserting on the
document in a test.


--------------------------------------------------------------------------------
title: "Testing"
description: "Serve an application in-process over an in-memory network and exercise middleware, routing, binding, dependencies and error rendering together."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/fundamentals/testing"
--------------------------------------------------------------------------------

# Testing

`muzak.dev/framework/testclient` serves an application in-process and issues real requests against it,
so a test exercises the whole stack rather than any one part of it in isolation.

```go [handlers/items_test.go]
package handlers_test

import (
    "net/http"
    "testing"

    "muzak.dev/framework/testclient"
)

func TestReadItem(t *testing.T) {
    client := testclient.New(t, buildApp())

    res := client.Get("/items/foo", testclient.Header("X-Token", "coneofsilence"))

    res.AssertStatus(http.StatusOK)
    res.AssertJSON(`{"id":"foo","title":"Foo","description":"There goes my hero"}`)
}
```

Requests travel over an in-memory network rather than a real socket, so a test needs no
free port and cannot be disturbed by anything else on the machine.

## What `New` does

```go
client := testclient.New(t, buildApp())
```

The application is built, its lifecycle components are started, and both the server and
those components are released through `t.Cleanup` when the test finishes. A build failure
or a component that refuses to start fails the test immediately, because every later
assertion would be meaningless.

A cookie jar is enabled by default, which lets a login followed by an authenticated call
work the way it would in a browser.

| Client option | Effect |
|---|---|
| `WithHeader(name, value)` | A header sent with every request, so a token is not repeated on each call |
| `WithTimeout(d)` | Bounds a single request, defaulting to ten seconds |
| `WithoutCookies()` | Disables the jar, so each request is independent |
| `WithoutRedirects()` | Stops the client following redirects, so a test can assert on the 3xx itself |

## Building the application under test

Write a helper that returns the application, and quiet its logger.

```go [handlers/helpers_test.go]
// buildApp returns an application with a shared secret guard and a small
// in-memory store.
func buildApp() *muzak.App {
    store := core.NewItemStore()

    app := muzak.New(muzak.AppOptions{
        Title:         "Awesome API",
        Version:       "1.0.0",
        LoggerOptions: muzak.LoggerOptions{Format: muzak.LogFormatNone},
    },
        muzak.WithDependencies(muzak.RequireHeaderToken("X-Token", "coneofsilence")),
        muzak.WithSingleton(store, store.Lifecycle()),
    )

    app.Include(routers.Items())
    return app
}

// authorized is the header every request in this suite needs.
func authorized() testclient.Option {
    return testclient.WithHeader("X-Token", "coneofsilence")
}
```

Because `main` does nothing but compose, the same routers, handlers and dependencies the
real service uses are what the test exercises.

## Issuing requests

```go
client.Get("/items/foo")
client.Post("/items/", testclient.JSON(item))
client.Put("/items/foo", testclient.JSON(rename))
client.Patch("/items/foo", testclient.JSON(patch))
client.Delete("/items/foo")
client.Head("/items/foo")
client.Options("/items/")
client.Do(http.MethodPropfind, "/items/")
```

| Request option | Effect |
|---|---|
| `JSON(value)` | Encodes the value as a JSON body and sets `Content-Type` |
| `RawJSON(body)` | Sends the body verbatim, for JSON a Go value could not produce |
| `Body(contentType, r)` | An arbitrary body under a given content type |
| `Header(name, value)` | Sets a header on this request, replacing any client-level value |
| `Query(name, value)` | Adds a query parameter, repeatable to send several values |
| `Cookie(c)` | Sends a cookie in addition to whatever the jar holds |
| `Subprotocols(...)` | Offers WebSocket subprotocols on a `WS` call |
| `KeepComments()` | Delivers an event stream's comment lines as messages of their own |

## Asserting on a response

```go
func TestReadNonexistentItem(t *testing.T) {
    client := testclient.New(t, buildApp(), authorized())

    res := client.Get("/items/baz")

    res.AssertStatus(http.StatusNotFound)
    if got := res.Error().Error.Message; got != "Item not found" {
        t.Errorf("message = %q", got)
    }
}
```

| Assertion | Checks |
|---|---|
| `AssertStatus(want)` | The status code |
| `AssertJSON(want)` | The body, compared semantically so member order and whitespace do not matter |
| `AssertHeader(name, want)` | One response header |
| `AssertErrorCode(want)` | The `code` of an error envelope, such as `validation_error` |

Every assertion returns the response, so they chain:

```go
client.Get("/items/foo").
    AssertStatus(http.StatusOK).
    AssertHeader("Content-Type", "application/json; charset=utf-8").
    AssertJSON(`{"id":"foo","title":"Foo","description":"There goes my hero"}`)
```

A `Response` carries `Status`, `Header`, `Body` and `Cookies` directly, is read fully into
memory so it can be inspected more than once, and offers `RequestID()`, `String()`,
`JSON(target)` and `Error()`.

## Decoding into a typed value

```go
item := client.Get("/items/foo").Decode[ItemOut]()
if item.Title != "Foo" {
    t.Errorf("title = %q, want %q", item.Title, "Foo")
}
```

The type argument is written at the call site, which keeps the expected shape visible in
the test and checked by the compiler. `testclient.Decoded[ItemOut](client.Get("/items/foo"))`
is the free-function form, for chaining directly off a request.

## Testing failures

```go
func TestCreateExistingItem(t *testing.T) {
    client := testclient.New(t, buildApp(), authorized())

    res := client.Post("/items/", testclient.RawJSON(
        `{"id":"foo","name":"The Foo ID Stealers"}`))

    res.AssertStatus(http.StatusConflict)
    res.AssertErrorCode(muzak.CodeConflict)
}

func TestReadItemBadToken(t *testing.T) {
    client := testclient.New(t, buildApp())

    res := client.Get("/items/foo", testclient.Header("X-Token", "hailhydra"))

    res.AssertStatus(http.StatusUnauthorized)
    res.AssertErrorCode(muzak.CodeUnauthorized)
}
```

`RawJSON` is what tests a body a Go value could not produce, such as a duplicate member, an
unknown member or a malformed document:

```go
res := client.Post("/items/", testclient.RawJSON(`{"id":"foo","nmae":"Foo"}`))

res.AssertStatus(http.StatusUnprocessableEntity)
if got := res.Error().Error.Details[0].Issue; got != "is not a field this endpoint accepts" {
    t.Errorf("issue = %q", got)
}
```

## Testing another language

A locale is chosen by a header, so testing one is a header on the request. The response
reports what it answered in, which is worth asserting alongside the body: a test that only
checks the words passes for the right reason and for the wrong one.

```go
func TestRejectionInSpanish(t *testing.T) {
    client := testclient.New(t, buildApp(t))

    res := client.Post("/items/",
        testclient.Header("Accept-Language", "es"),
        testclient.RawJSON(`{"name":""}`))

    res.AssertStatus(http.StatusUnprocessableEntity)
    res.AssertHeader("Content-Language", "es")

    if got := res.Error().Error.Details[0].Issue; got != "es obligatorio" {
        t.Errorf("issue = %q", got)
    }
}
```

An application that has not been localized needs no change to its tests. With no store
configured every message reads exactly as it did, which is the property worth having a test
of its own for.

## Sessions and cookies

The jar carries whatever a login established, so a flow reads the way it happens.

```go
func TestLoginThenRead(t *testing.T) {
    client := testclient.New(t, buildApp())

    form := url.Values{"username": {"muzak"}, "password": {"correct-horse-battery"}}
    client.Post("/login/", testclient.Body(
        "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))).
        AssertStatus(http.StatusOK)

    client.Get("/feed").AssertStatus(http.StatusOK)
}
```

## WebSockets

```go
func TestItemSocket(t *testing.T) {
    client := testclient.New(t, buildApp())

    conn := client.WS("/items/plumbus/ws", testclient.Query("token", "jessica"))

    if err := conn.WriteText(t.Context(), "hello"); err != nil {
        t.Fatalf("WriteText = %v", err)
    }
    reply, err := conn.ReadText(t.Context())
    if err != nil {
        t.Fatalf("ReadText = %v", err)
    }
    if !strings.Contains(reply, "jessica") {
        t.Errorf("reply = %q", reply)
    }
}
```

`Client.WS` fails the test if the handshake is refused. The connection is closed when the
test finishes, so a handler blocked on a read is released even if the test forgets.

To assert on a handshake that is *meant* to be refused, use `TryWS`, which returns whatever
came back:

```go
_, response := client.TryWS("/items/plumbus/ws")
response.AssertStatus(http.StatusUnauthorized)
```

The connection is a real `*muzak.WSConn`, driven by the same engine the server uses, so a
route is tested end to end rather than against a second implementation.

## Server-sent events

```go
func TestStreamItems(t *testing.T) {
    client := testclient.New(t, buildApp())

    stream := client.SSE("/items/stream", testclient.Query("token", "jessica"))

    client.Post("/items/", testclient.Query("token", "jessica"),
        testclient.JSON(map[string]string{"id": "plumbus", "name": "Plumbus"})).
        AssertStatus(http.StatusCreated)

    item := stream.Decode[ItemOut]()
    if item.ID != "plumbus" {
        t.Errorf("id = %q, want plumbus", item.ID)
    }
}
```

| Call | Effect |
|---|---|
| `stream.Next()` | The next event, failing the test if the stream ends or fails first |
| `stream.Decode[T]()` | The next event's data decoded into `T` |
| `stream.TryNext()` | The next event and whatever ended the stream, for a test that expects an ending |
| `stream.LastEventID()` | The identifier of the last event that carried one |
| `stream.Close()` | Ends the stream, which is how a test checks that the handler notices |
| `stream.Response` | The status, headers and cookies the stream opened with |

Reads are bounded by the client's timeout, so a stream that never sends the event a test is
waiting for fails the test rather than hanging it.

```go
_, err := stream.TryNext()
if !errors.Is(err, muzak.ErrSSEStreamEnded) {
    t.Fatalf("err = %v, want the stream to have ended", err)
}
```

`Client.SSEDo` opens a stream with another method, which is what a stream answering a
posted document takes, and `Client.TrySSE` is the counterpart of `TryWS`:

```go
stream := client.SSEDo(http.MethodPost, "/chat/stream", testclient.JSON(prompt))
```

```go
_, response := client.TrySSE(http.MethodGet, "/items/stream")
response.AssertStatus(http.StatusServiceUnavailable)
```

Resuming a dropped stream is a header:

```go
resumed := client.SSE("/items/stream", testclient.Header("Last-Event-ID", stream.LastEventID()))
```

## Testing a handler on its own

A handler is an ordinary typed function, so a unit test can call it directly with a
constructed input, with no router, server or HTTP request in sight. That works whenever the
handler reads nothing from the context. A handler that calls `muzak.From` needs the
dependency, which is what the test client provides.

Validation rules are testable the same way, without a request at all:

```go
if err := validateTag().Check("Not A Tag"); err == nil {
    t.Error("want a spaced tag to be rejected")
}

if err := NotACommonPassword("password1234"); err == nil {
    t.Error("want a common password to be rejected")
}
```

## Catching configuration errors in a test

```go
func TestAppBuilds(t *testing.T) {
    if err := buildApp().Build(); err != nil {
        t.Fatalf("Build = %v", err)
    }
}
```

`Build` reports every route problem at once: a duplicate path, an unbindable input type, a
path parameter no field binds, a duplicate operation identifier. One test keeps all of them
out of a deployment.

## Concurrency

A `Client` is safe for concurrent use, which lets a test fire parallel requests to check
that request-scoped state stays isolated.

```go
func TestConcurrentRequestsStayIsolated(t *testing.T) {
    client := testclient.New(t, buildApp(), authorized())

    var wg sync.WaitGroup
    for i := range 50 {
        wg.Add(1)
        go func() {
            defer wg.Done()
            client.Get("/items/foo").AssertStatus(http.StatusOK)
            _ = i
        }()
    }
    wg.Wait()
}
```

```bash
go test ./... -race
```

## Where to go next

[Lifecycle](/docs/getting-started/lifecycle) covers the components the client
starts for you, and [Configuration](/docs/fundamentals/configuration) covers
pinning settings a test depends on.


--------------------------------------------------------------------------------
title: "Internationalization"
description: "A locale resolved once per request from the Accept-Language header or wherever else you say, with every string in the response written in it, including the ones the framework produces itself."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/fundamentals/internationalization"
--------------------------------------------------------------------------------

# Internationalization

A request's locale is resolved once, before the handler runs, and every string in the
response is written in it. That includes the strings Muzak produces: the wording of each
validation rule, what the request binder says about a value it could not read, and the
sentence behind each HTTP status.

::code-group

```go [core/i18n.go]
//go:embed locales
var locales embed.FS

func LocaleOptions() muzak.I18nOptions {
    return muzak.I18nOptions{Store: i18n.MustLoad(locales, "locales")}
}
```

```go [cmd/main.go]
app := muzak.New(muzak.AppOptions{
    I18n: core.LocaleOptions(),
})
```

```yaml [core/locales/es.yml]
es:
  greeting:
    hello: "Hola, %{name}"
    items:
      zero: "No tienes artículos"
      one: "Tienes un artículo"
      other: "Tienes %{count} artículos"
```

::

A handler translates through its context, which already knows the locale.

```go [handlers/greeting.go]
func Greeting(ctx *muzak.Context, in schemas.GreetingParams) (schemas.GreetingOut, error) {
    return schemas.GreetingOut{
        Locale:   ctx.Locale(),
        Greeting: ctx.T("greeting.hello", "name", in.Name),
        Items:    ctx.T("greeting.items", "count", in.Items),
        Today:    ctx.L(time.Now(), "as", "date", "format", "long"),
    }, nil
}
```

```
$ curl -H 'Accept-Language: es' 'localhost:8080/greeting?name=Ada&items=1'
{"locale":"es","greeting":"Hola, Ada","items":"Tienes un artículo",
 "today":"24 de agosto de 2026"}
```

## A locale is passed, never set

Go has no per-goroutine storage, and a package-level locale shared by every goroutine would
leak one request's language into another: a Spanish request setting it, and an English one
two microseconds later reading it. On a server handling more than one request at a time,
which is every server, a current locale is not a thing that can exist.

So the locale is passed. Muzak resolves it once per request, carries it on the request's
context, and `ctx.T` reads it from there. Code with no request in hand names the locale
itself.

```go
i18n.T("es", "greeting.hello", "name", "Ada")
```

Anywhere there is a `context.Context` but no Muzak one, such as a repository or a goroutine
started from a handler, the same value is one call away.

```go
locale, ok := muzak.LocaleFromContext(ctx)
```

## Where the locale comes from

The choice is usually written by hand in a filter that runs per request. Here it is
declared, so it is visible in the application's options rather than buried in code. Sources are tried in order, and the
first that yields a locale the application actually answers in wins.

```go
muzak.I18nOptions{
    Store: i18n.MustLoad(locales, "locales"),
    Sources: []muzak.LocaleSource{
        muzak.LocaleFromQuery,
        muzak.LocaleFromAcceptLanguage,
    },
}
```

| Source | Reads | Configured by |
|---|---|---|
| `LocaleFromAcceptLanguage` | The `Accept-Language` header, quality values honoured | Nothing. It is the default |
| `LocaleFromPath` | A path segment, as `/pt/books` | `PathIndex`, counting from zero |
| `LocaleFromQuery` | A query parameter | `Query`, defaulting to `locale` |
| `LocaleFromHeader` | A named header | `Header`, defaulting to `X-Locale` |
| `LocaleFromCookie` | A cookie | `Cookie`, defaulting to `locale` |
| `LocaleFromCustom` | Whatever you write | `Extractor` |

`LocaleFromAcceptLanguage` is the default because every browser sends the header and nothing
has to be added to a URL for it to work. The client's quality values order the candidates, a
range matches by language as well as in full, so `pt-BR` is served `pt` when that is what
you have, and `en;q=0` refuses English outright even when English is all there is.

A locale in a cookie is invisible in the URL, so two people opening the same link see
different pages. Prefer a source that travels with the address unless you have a reason not
to.

### Nothing a request says is trusted

A value arriving from a client is matched against the locales the application declared, and
discarded if it matches none. What comes out of resolution is always one of your own
strings, never one of theirs, which is the single rule that keeps `?locale=../../etc/passwd`
out of a filesystem path and a locale carrying a line break out of a response header.

## Locale files

YAML or JSON, with the locale at the top level. Files are merged key by key rather than
file by file, so a locale may be spread across as many files as suits it.

```yaml [core/locales/es.yml]
es:
  errors:
    messages:
      blank: "es obligatorio"
      too_short:
        one: "debe tener al menos 1 carácter"
        other: "debe tener al menos %{count} caracteres"
```

Muzak has no third-party dependencies, so the YAML is read by a parser written for this and
nothing else. It reads the subset locale files are written in, which is deliberately the
subset the published locale corpora use, so a file taken from one loads here unchanged,
formats and plural forms included. Anything
outside that subset is refused with the line and column it was found at, rather than
guessed at.

Files are embedded rather than read from disk by default. A service that shipped without its
locale directory would start cleanly and then answer every request in the wrong language,
which is the kind of failure that reaches production. Point `Dir` at a directory instead
when translations should be editable without a rebuild.

## What you have to translate, and what you do not

A locale file names the strings your service adds and the rules you want worded differently.
Everything else falls through to the locale Muzak ships, which covers every string the
framework produces. A translation grows as a service is translated rather than having to be
complete before it is useful.

```
$ curl -H 'Accept-Language: es' localhost:8080/items -d '{"name": "", "cost": 10, "price": 5}'
{"error":{
  "code":"validation_error",
  "message":"La solicitud no pudo ser validada.",
  "status":422,
  "details":[
    {"field":"name","location":"body","issue":"cada artículo necesita un nombre"},
    {"field":"price","location":"body","issue":"el precio no puede ser inferior al coste"}
  ]}}
```

The code is not translated, and never will be. Clients branch on it, so a locale file that
reworded one would break every client reading it.

### Four scopes, narrowest first

A failure is looked up under four keys, in order, so one rule on one field of one model can
be phrased without restating any other.

```
errors.models.<model>.attributes.<field>.<rule>
errors.models.<model>.<rule>
errors.attributes.<field>.<rule>
errors.messages.<rule>
```

```yaml
es:
  errors:
    messages:
      blank: "es obligatorio"
    models:
      create_item:
        attributes:
          name:
            blank: "cada artículo necesita un nombre"
```

The model is the input type's name in snake case. A framework that distinguishes a
persisted record from a plain model needs a fifth level above these; Muzak has one kind of
model, so it has four.

### Naming a translation yourself

`Message` fixes the wording in one language on purpose. `MessageKey` names a translation
instead, and the rule's own values still fill it in.

```go [schemas/items.go]
func (in *CreateItem) Validate(v *muzak.Validation) {
    v.String(&in.Password).MinLen(12).MessageKey("errors.password.too_short")

    v.When(in.Price < in.Cost).
        RejectKey(&in.Price, "errors.item.price_below_cost")
}
```

An error you raise yourself works the same way, keeping the message already written as the
fallback.

```go
return muzak.Forbidden("only editors may do that").
    WithMessageKey("errors.access.denied")
```

## Pluralization

A count both prints and chooses which wording prints it.

```yaml
en:
  greeting:
    items:
      zero: "You have no items"
      one: "You have one item"
      other: "You have %{count} items"
```

```go
ctx.T("greeting.items", "count", 0)  // You have no items
ctx.T("greeting.items", "count", 1)  // You have one item
ctx.T("greeting.items", "count", 5)  // You have 5 items
```

English has two forms and an optional zero. Russian has four, Arabic six, and Japanese one.
Muzak carries the CLDR arithmetic for around ninety languages, so a locale file supplies only
the words.

```yaml
ru:
  files:
    one: "%{count} файл"
    few: "%{count} файла"
    many: "%{count} файлов"
```

A rule that is genuinely new is conventionally written as a function inside the locale
data, which a YAML file in Go cannot hold. A locale file here names a rule instead, and a
rule nobody has written yet is supplied as a function.

::code-group

```yaml [naming one]
ru:
  i18n:
    plural:
      rule: slavic
```

```go [writing one]
i18n.StoreOptions{
    PluralRules: map[string]i18n.PluralRule{
        "xx": func(n int) i18n.PluralCategory { ... },
    },
}
```

::

## Interpolation

`%{name}` takes a value as it comes; `%<name>d` takes a format directive.

```yaml
en:
  product_price: "$%{price}"
  progress: "%<done>03d of %<total>03d"
```

```go
ctx.T("product_price", "price", 10)
ctx.T("progress", "done", 7, "total", 250)
```

Arguments are alternating names and values, the way `slog` reads them, because a translation
takes an open set of values under names the translator chose and Go has no keyword
arguments. A handful of names mean something to the lookup rather than to the sentence:
`count`, `scope`, `default` and `locale`. A translation that names `scope` or `default` is an
error rather than a silent substitution.

A value the translation expects and the call did not supply is also an error. A message
missing a number is worse than no message, because it looks correct.

## Dates and numbers

`ctx.L` writes a value the way the locale writes it.

```go
ctx.L(time.Now())                          // Mon, 24 Aug 2026 09:05:03 +0000
ctx.L(time.Now(), "as", "date", "format", "long")
ctx.L(1234.5, "precision", 2)              // 1,234.50 in en, 1.234,50 in es
```

```yaml
es:
  date:
    formats:
      long: "%d de %B de %Y"
    month_names: [~, enero, febrero, marzo, abril, mayo, junio, julio, agosto,
                  septiembre, octubre, noviembre, diciembre]
  number:
    format:
      separator: ","
      delimiter: "."
```

Patterns are strftime rather than Go layouts, and that is not a matter of taste. Go formats
a time by example, and the reference layout hard-codes `January`, so a Go layout can only
ever produce an English month name. A strftime pattern names the field, which leaves Muzak
free to fill it from `date.month_names` in whatever locale was asked for. It is also what the
published locale corpora are written in.

A pattern beginning `go:` is handed to Go's own formatter, for the formats meant to be read
by a machine.

```yaml
en:
  date:
    formats:
      iso: "go:2006-01-02T15:04:05Z07:00"
```

The number helpers from the same scope are on the store: `NumberWithDelimiter`,
`NumberWithPrecision`, `NumberToCurrency`, `NumberToPercentage`, `NumberToHumanSize`,
`DistanceOfTimeInWords` and `ToSentence`.

## Caching

A response reports the locale it was written in, and says so in `Vary` when the header took
part in choosing it.

```
Content-Language: es
Vary: Accept-Language, Accept-Encoding
```

Without the `Vary`, a cache in front of the service will serve one language to a client that
asked for another. It is added by default and `DisableVary` turns it off, for a service whose
cache key is already settled by something else.

## Leaving it out

Leave `AppOptions.I18n` unset and none of this exists. No middleware is installed, no locale
is resolved, and every message reads exactly as it does without this feature.

It goes further than configuration. The framework reaches a translation engine only through
an interface, so a binary that never mentions `muzak.dev/framework/i18n` links neither the
engine, nor the YAML parser, nor the table of plural rules. A service that answers in one
language does not carry the machinery for ninety.

```go
type Translator interface {
    Translate(locale, key string, args ...any) string
    Localize(locale string, value any, args ...any) string
    Exists(locale, key string) bool
    AvailableLocales() []string
    DefaultLocale() string
}
```

Anything satisfying it can be the store, which is how translations come from a database or a
service rather than from a file.

## Running this at scale

Nothing here holds per-process state that a request can change, which is what makes it safe
on a platform that runs many requests at once on an instance it may destroy a second later.

A locale is resolved from the request, put on the request's context, and copied onto the
pooled `Context` when that is taken from the pool. It is cleared when the `Context` goes
back. Nothing writes to the store after start-up: the translations, the plural rules and
the fallback chains are all settled while the application is being built, and only read
afterwards. The tests run 64 goroutines through five locales under the race detector, half
of them naming no locale at all, and require every answer to match its own request.

### Cold start

On this machine, building an application with five locale files costs about **83
microseconds** more than building the same application without translations, and about
**69 kilobytes**. The rest of a cold start is the framework and the runtime.

```
BenchmarkColdStartWithoutI18n     1.007 ms      6.90 MB
BenchmarkColdStart                1.090 ms      6.97 MB
BenchmarkStoreOnly                0.048 ms      0.07 MB
```

Locale files are parsed once, when the store is built, and the locale the framework ships
is parsed once per process and shared by every store after that. A warm request that
translates a message with nothing to interpolate allocates nothing at all.

### Embed the locale files

Use `FS` rather than `Dir` anywhere the filesystem is not yours. A container built from
scratch has no locale directory, and an instance that starts without one will serve every
request in the default language rather than failing, which is the kind of fault that is
noticed a week later by a customer.

```go
//go:embed locales
var locales embed.FS
```

### Time zones need a database

`Timezone()` resolves a name against the zone database, and a Go binary does not carry one
unless it is asked to. On a scratch or distroless image there is no `/usr/share/zoneinfo`
either, so the rule refuses every value including the correct ones, and nothing says why.

Either carry the database, at a cost of about 450 kilobytes:

```go
import _ "time/tzdata"
```

or run on an image that has one. Check at start-up either way, so that a missing database
stops the process rather than every request:

```go
if !validate.TimezoneDataAvailable() {
    log.Fatal("no time zone database: import _ \"time/tzdata\" or use an image that has one")
}
```

### Caches in front of the service

A response says which language it is in and, when the header decided it, that the header
decided it.

```
Content-Language: es
Vary: Accept-Language, Accept-Encoding
```

Without the `Vary`, a shared cache will hand one language to a client that asked for
another. It is written by default; `DisableVary` turns it off for a service whose cache key
is already settled some other way.

## One thing deliberately left out

`errors.format`, and the full messages built from it, have no counterpart here.

The idea is to join an attribute name to a message to make one sentence, as "Email is
required". Muzak reports `field`, `location` and `issue` as separate members of the
envelope and never concatenates them, so there is nothing for a format to join. A client
that wants the sentence builds it from the three, in whatever order its own language puts
them, which is the part a fixed format gets wrong outside English.

Everything else is here: scopes, default chains including keys that name other keys, bulk
lookup, fallbacks, swappable and chained backends, and the exception handlers.

## Where to go next

- [Validation](/docs/fundamentals/validation) for the rules whose messages these are.
- [Error handling](/docs/getting-started/error-handling) for the envelope they arrive in.
- [Middleware](/docs/getting-started/middleware) for where the locale is resolved in the chain.


--------------------------------------------------------------------------------
title: "Headers"
description: "Bind request headers into typed fields, read them directly, and set response headers that a cache and a client can rely on."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/techniques/headers"
--------------------------------------------------------------------------------

# Headers

A header is bound like any other parameter. Tag a field with `header:"Name"` and it is read
from the request, converted to the field's type, and documented.

```go
type ClientHeaders struct {
    // Host is the authority the client addressed, which is what absolute URLs
    // in the response are built from.
    Host string `header:"Host" doc:"The authority the request was addressed to"`

    // SaveData is the client's data saver preference. It is the string "on"
    // when set, not a boolean, so it is bound as one.
    SaveData string `header:"Save-Data" default:"off" doc:"Set to on by a client asking for a smaller payload"`

    // Traceparent carries the caller's trace context, echoed back so a client
    // can correlate its span with this response.
    Traceparent string `header:"traceparent" doc:"W3C trace context of the calling span"`

    // Tags filter the feed. The header may be repeated, and every value is
    // bound in the order it arrived.
    Tags []string `header:"X-Tag" doc:"Restrict the feed to these tags; may be repeated"`
}
```

Names are matched case-insensitively, as HTTP requires, so `traceparent` and `Traceparent`
name the same header.

A header is optional unless the field says `required:"true"`, and a `default` supplies the
value when the client sends nothing.

```go
type PagingHeaders struct {
    APIVersion string `header:"X-API-Version" required:"true" doc:"The API version this client was written against"`
    PageSize   int    `header:"X-Page-Size" default:"50"`
}
```

## Repeated headers

Declare a slice, and every value arrives in the order it was sent. A non-slice field takes
the first value.

```bash
curl 'http://localhost:8080/feed' -H 'X-Tag: go' -H 'X-Tag: http'
```

```go
in.Tags // []string{"go", "http"}
```

## Headers that are not plain text

A header whose format is not one Go already parses gets a type of its own. Implementing
`encoding.TextUnmarshaler` is all it takes.

```go [schemas/feed.go]
// HTTPDate is a time carried in the format HTTP dates use.
//
// It exists because If-Modified-Since is not RFC 3339, which is what a bare
// time.Time parses.
type HTTPDate struct {
    time.Time
}

// UnmarshalText parses an HTTP date, as RFC 9110 defines it.
func (d *HTTPDate) UnmarshalText(text []byte) error {
    parsed, err := http.ParseTime(string(text))
    if err != nil {
        return errors.New("must be an HTTP date, such as Wed, 21 Oct 2026 07:28:00 GMT")
    }
    d.Time = parsed
    return nil
}
```

```go
type ClientHeaders struct {
    IfModifiedSince HTTPDate `header:"If-Modified-Since" doc:"Answer 304 when nothing changed since this time"`
}
```

The error the method returns is the issue the client sees, so phrase it to read after the
field name:

```json
{ "field": "If-Modified-Since", "location": "header",
  "issue": "must be an HTTP date, such as Wed, 21 Oct 2026 07:28:00 GMT" }
```

## Validating a header

Rules bind to the field, not to the location, so a header is checked exactly as a query
parameter is and its failure is reported against the header name the client sent.

```go
func (in *FeedIn) Validate(v *muzak.Validation) {
    v.String(&in.SaveData).Trim().Lower().OneOf("on", "off")
    v.String(&in.Traceparent).Matches(`^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`).
        Message("must be a W3C trace context, such as 00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
    v.Slice(&in.Tags).MaxItems(5).Each(validate.String().MaxLen(24))
}
```

Transforms run before the checks, which is what lets `Save-Data` be compared against a
single spelling in the handler afterwards.

## Reading a header directly

```go
ctx.Header("X-Request-Id")            // first value, or "" when absent
ctx.Request().Header.Values("X-Tag")  // every value, through net/http
```

Prefer binding. A bound header is typed, documented, validated and reported the same way
every other parameter is; a header read by hand is none of those things.

## Setting response headers

```go
func Feed(ctx *muzak.Context, in schemas.FeedIn) (schemas.FeedOut, error) {
    // The caller's trace context is echoed so its span and this response can be
    // tied together, and the request identifier is what ties it to our log.
    if in.Traceparent != "" {
        ctx.SetHeader("traceparent", in.Traceparent)
    }
    // The response varies by all three, so caches must be told.
    ctx.SetHeader("Vary", "Save-Data, X-Tag, Cookie")
    ctx.SetHeader("Last-Modified", feedUpdatedAt.UTC().Format(http.TimeFormat))

    // ...
}
```

`SetHeader` replaces any value already present. `AddHeader` appends, which is what repeated
headers require:

```go
ctx.AddHeader("Vary", "Accept-Encoding")
ctx.AddHeader("Link", `</items?page=2>; rel="next"`)
```

Headers must be set before the handler returns. Once the response has begun, `net/http`
ignores further changes, which is the same rule that governs
[middleware](/docs/getting-started/middleware).

## Conditional responses

```go
func Feed(ctx *muzak.Context, in schemas.FeedIn) (schemas.FeedOut, error) {
    // A conditional request is answered without a body when nothing changed.
    // The header was parsed into a time by the binder, so this is a comparison
    // rather than a parse that could fail here.
    if !in.IfModifiedSince.IsZero() && !feedUpdatedAt.After(in.IfModifiedSince.Time) {
        ctx.SetStatus(http.StatusNotModified)
        return schemas.FeedOut{}, nil
    }
    // ...
}
```

`304` writes no body at all, whatever the handler returned. Document the outcome so it
appears in the generated reference, with the model that says the response is empty:

```go
r.Get("/feed", handlers.Feed,
    muzak.WithResponseModel[muzak.Empty](http.StatusNotModified, "The feed has not changed since If-Modified-Since"))
```

`WithResponseDoc` would have described that status as the error envelope, which is right
for an outcome a handler reports by returning an error and wrong for this one. See
[Response Models](/docs/techniques/response-models).

## Headers Muzak sets for you

| Header | Set by | Value |
|---|---|---|
| `X-Request-Id` | `RequestID` | The identifier assigned to the request |
| `X-Content-Type-Options` | `SecurityHeaders` | `nosniff` |
| `X-Frame-Options` | `SecurityHeaders` | `DENY` |
| `Referrer-Policy` | `SecurityHeaders` | `strict-origin-when-cross-origin` |
| `Content-Type`, `Content-Length` | the response encoder | For the body it wrote |
| `Allow` | the router | On an automatic `OPTIONS` and on a `405` |
| `Vary` | `Compress` | `Accept-Encoding`, on every response |
| `Retry-After`, `RateLimit-*` | the rate limiter | On a refusal, and on every counted request |

`SecurityHeaders` never overwrites a value already set, so a handler or a later middleware
can opt out per response.

```go
// This one page is allowed to be framed by the parent site.
ctx.SetHeader("X-Frame-Options", "SAMEORIGIN")
```

Turn the whole set off with `AppOptions.DisableSecurityHeaders` if something in front of
the service sets them instead.

## Forwarding headers

`X-Forwarded-For` and its relatives are not believed by default, because a header any
client can write is an identity any client can claim. Naming the proxy is what makes it
believable. See [Behind a Proxy](/docs/deployment/behind-a-proxy).

## Where to go next

[Cookies](/docs/techniques/cookies) covers the other half of what a browser
sends, and [Compression](/docs/techniques/compression) covers the `Vary` header
that goes with it.


--------------------------------------------------------------------------------
title: "Cookies"
description: "Bind cookies into typed fields, set them on a response, and choose the attributes that decide whether a session is safe."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/techniques/cookies"
--------------------------------------------------------------------------------

# Cookies

A cookie is bound like any other parameter.

```go [schemas/feed.go]
// SessionCookies groups the cookies the browser sends back.
type SessionCookies struct {
    // SessionID identifies the signed-in reader and must be present.
    SessionID string `cookie:"session_id" required:"true" doc:"The reader's session"`

    // The trackers are declared so they are documented and so a reader can be
    // told what is being read, not because the feed needs them.
    FatebookTracker string `cookie:"fatebook_tracker" doc:"Third party analytics cookie, if the reader accepted one"`
    GoogallTracker  string `cookie:"googall_tracker" doc:"Third party analytics cookie, if the reader accepted one"`
}
```

Embed that group into any input that needs it:

```go
type FeedIn struct {
    ClientHeaders
    SessionCookies

    Limit int `query:"limit" default:"20" doc:"How many entries to return"`
}
```

A cookie is optional unless the field says `required:"true"`. A missing required cookie is
a `422` naming it:

```json
{ "field": "session_id", "location": "cookie", "issue": "is required" }
```

Cookies are converted by the same setters every other parameter uses, so a cookie holding a
number or a UUID can be bound as one.

```go
type PreferenceCookies struct {
    Theme     string    `cookie:"theme" default:"system"`
    PageSize  int       `cookie:"page_size" default:"25"`
    DeviceID  uuid.UUID `cookie:"device_id"`
}
```

Validate them the way you validate anything else:

```go
func (in *FeedIn) Validate(v *muzak.Validation) {
    v.String(&in.SessionID).Trim().MinLen(4).MaxLen(64)
    v.String(&in.Theme).OneOf("system", "light", "dark")
}
```

## Reading one directly

```go
cookie, err := ctx.Cookie("session")
if errors.Is(err, http.ErrNoCookie) {
    // nothing was sent under that name
}
```

That is what a dependency resolving a session does, because it runs before binding and
serves several routes at once:

```go [core/dependencies.go]
func GetSessionOrToken(ctx *muzak.Context) (SessionOrToken, error) {
    if cookie, err := ctx.Cookie("session"); err == nil && cookie.Value != "" {
        return SessionOrToken{Value: cookie.Value, FromCookie: true}, nil
    }
    if token := ctx.Query("token"); token != "" {
        return SessionOrToken{Value: token}, nil
    }
    return SessionOrToken{}, muzak.Unauthorized("a session cookie or a token query parameter is required")
}
```

## Setting a cookie

```go [handlers/login.go]
session := uuid.NewV4().String()
ctx.SetCookie(&http.Cookie{
    Name:  "session_id",
    Value: session,
    Path:  "/",
    // HttpOnly keeps the session out of reach of scripts, and SameSite keeps
    // it off cross-site requests. Secure belongs here too once this is served
    // over TLS, which is why it is named rather than omitted.
    HttpOnly: true,
    SameSite: http.SameSiteLaxMode,
    Secure:   false,
    MaxAge:   3600,
})
```

`SetCookie` adds a `Set-Cookie` header for the cookie as given. Muzak does not modify it:
the attributes are the caller's to choose, and choosing them is the whole security
decision.

| Attribute | Why it matters |
|---|---|
| `HttpOnly` | Keeps the value out of reach of scripts, so a cross-site scripting bug cannot read the session |
| `Secure` | Refuses to send the cookie over plain HTTP. Set it wherever the service is served over TLS |
| `SameSite` | `Lax` stops the cookie riding along on cross-site requests, which is most of what CSRF needs |
| `Path` | Narrows where the cookie is sent. `/` is right for a session, narrower is better for anything else |
| `MaxAge` | Bounds how long the credential is useful. A session with no expiry is a credential with no expiry |

A session cookie is exactly the kind of ambient authority a cross-origin WebSocket
handshake can borrow, which is why the origin check in
[WebSockets](/docs/realtime/websockets) exists and why `AppOptions.CORS` cannot
substitute for it.

## Clearing a cookie

Send it back with an expiry in the past and the same `Path`, so the browser drops it.

```go
func Logout(ctx *muzak.Context, _ muzak.Empty) (muzak.Empty, error) {
    ctx.SetCookie(&http.Cookie{
        Name:     "session_id",
        Value:    "",
        Path:     "/",
        HttpOnly: true,
        SameSite: http.SameSiteLaxMode,
        MaxAge:   -1,
    })
    return muzak.Empty{}, nil
}
```

## Several cookies on one response

`SetCookie` appends rather than replacing, so more than one is fine.

```go
ctx.SetCookie(&http.Cookie{Name: "session_id", Value: session, Path: "/", HttpOnly: true})
ctx.SetCookie(&http.Cookie{Name: "theme", Value: in.Theme, Path: "/", MaxAge: 31536000})
```

## Cookies in logs

`cookie` and `set-cookie` are in `muzak.DefaultRedactedKeys`, so an attribute under either
key is replaced with `[redacted]` before a record is written. The access log records no
headers at all. See [Logging](/docs/fundamentals/logging).

## Cookies in tests

The test client keeps a cookie jar, so a login followed by an authenticated call works the
way it would in a browser.

```go
func TestLoginThenRead(t *testing.T) {
    client := testclient.New(t, buildApp())

    form := url.Values{"username": {"muzak"}, "password": {"correct-horse-battery"}}
    client.Post("/login/", testclient.Body(
        "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))).
        AssertStatus(http.StatusOK)

    // The jar carries the session_id cookie set above.
    client.Get("/feed").AssertStatus(http.StatusOK)
}
```

`testclient.WithoutCookies()` disables the jar when each request should be independent, and
`testclient.Cookie(c)` sends one cookie on a single request.

## Where to go next

[Authentication](/docs/security/authentication) covers what goes in a session
cookie, and [Forms and HTML](/docs/techniques/forms-and-html) covers the sign-in
form that sets one.


--------------------------------------------------------------------------------
title: "JSON"
description: "How request bodies are decoded and responses encoded, which struct tags matter, and why the defaults are strict rather than forgiving."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/techniques/json"
--------------------------------------------------------------------------------

# JSON

Muzak reads and writes JSON with `encoding/json/v2`. The defaults are strict, because a
request that is nearly right is a bug somewhere, and finding it in a `422` beats finding it
in a support ticket.

## Decoding a request

A field with no location tag is a member of the JSON body.

```go
type ItemCreateIn struct {
    ID    string `json:"id" doc:"The identifier to create the item under"`
    Name  string `json:"name" doc:"The item's display name"`
    Async bool   `json:"async,omitzero" doc:"Queue the work instead of doing it inline"`
}
```

What the decoder refuses:

| Refused | Why |
|---|---|
| An unknown member | A client's typo becomes an immediate `422` instead of a value that silently disappears |
| A duplicate member | Two values for one field means the client and the server disagree about which one won |
| Invalid UTF-8 | A string that is not text is not a string |
| A member of the wrong type | `"age": "42"` is not `"age": 42` |
| An empty body, when the route binds one | Reported as `{"field": "", "location": "body", "issue": "is required"}` |
| A media type that is not JSON | `415`, unless the type is `application/json`, ends in `+json`, or is absent |

```json
{
  "error": {
    "code": "validation_error",
    "message": "The request could not be validated.",
    "status": 422,
    "details": [
      { "field": "nmae", "location": "body", "issue": "is not a field this endpoint accepts" },
      { "field": "age",  "location": "body", "issue": "has the wrong type, a string is not accepted here" }
    ]
  },
  "request_id": "0611f4b2-2f0a-4b57-9c1a-6e6a2e2f9b31"
}
```

The Go type behind a failure is deliberately dropped from the message. Naming server-side
types in a response tells a client more about the implementation than it needs to know.

### Accepting unknown members

```go
r.Post("/webhooks/stripe", handlers.StripeWebhook, muzak.AllowUnknownFields())
```

Reach for it where forward compatibility with a client that sends extra members matters
more than catching typos, which is what a third-party webhook receiver looks like. It is a
shared option, so a whole router can carry it. Duplicate members and invalid UTF-8 stay
rejected either way.

### A body next to a path parameter

```go
type ItemRenameIn struct {
    ID   string `path:"item_id" doc:"The item to rename"`
    Name string `json:"name" doc:"The item's new display name"`
}
```

The body can only ever reach `Name`. When an input has any located field, the body is
decoded into a scratch value of the same type and only the body-bound fields are copied
out, so a request carrying `{"id": "somebody-elses-item"}` cannot overwrite the path
parameter.

## Encoding a response

The handler's return value is the body, serialized as it stands.

```go
type ItemOut struct {
    ID    string `json:"id" doc:"The item's identifier"`
    Name  string `json:"name" doc:"The item's display name"`
    Owner string `json:"owner,omitzero" doc:"The user the item belongs to"`
}
```

```json
{"id":"foo","name":"Foo"}
```

`Content-Type: application/json; charset=utf-8` and `Content-Length` are set for you. The
body is encoded fully into a pooled buffer before anything is written, so a failure part
way through encoding still produces a clean error response instead of a truncated body.

### The tags that matter

| Tag | Effect |
|---|---|
| `json:"name"` | The member name. Without a tag, the Go field name is used |
| `json:"name,omitzero"` | Omit the member when the value is the zero value of its type |
| `json:"-"` | Never encoded, and never a body member on the way in |
| `doc:"..."` | The member's description in the generated schema |
| `default:"..."` | The member's default in the generated schema, and the value used when a parameter is absent |

`omitzero` is what keeps a response clean without a pointer:

```go
Owner string `json:"owner,omitzero"`
```

```go [handlers/feed.go]
if trimmed {
    // The client asked for less, so the summary is left out. The field is
    // omitzero, so it disappears from the response entirely.
    entry.Summary = ""
}
```

It also decides requiredness in the generated schema: a member is required unless it is a
pointer, carries `omitzero` or `omitempty`, carries a `default`, or is tagged
`required:"false"`.

## Types on the wire

| Go type | JSON |
|---|---|
| `time.Time` | An RFC 3339 string, documented as `"format": "date-time"` |
| `uuid.UUID` | A string, documented as `"format": "uuid"` |
| a type implementing `encoding.TextMarshaler` and `encoding.TextUnmarshaler` | a string, both ways |
| a map | an object |
| a slice | an array |
| a pointer | the value, or `null` |
| an interface | whatever it holds, documented as accepting any JSON value |

A type that converts to and from text is a string everywhere: the binder parses it with
`UnmarshalText` in a parameter, the encoder writes it with `MarshalText` in a body, and the
schema calls it a string rather than describing the Go struct behind it.

```go
type Currency string

type PriceOut struct {
    Amount   int64    `json:"amount_minor"`
    Currency Currency `json:"currency"`
}
```

## Response models are not storage models

::code-group

```go [core/store.go]
// Item is one stored item. It is the store's own type, deliberately separate
// from the response models in the schemas package: a field added here does not
// become visible to clients until a handler puts it on a response type.
type Item struct {
    ID   string
    Name string
}
```

```go [handlers/items.go]
func ListItems(ctx *muzak.Context, _ muzak.Empty) (schemas.ItemListOut, error) {
    store := muzak.From[*core.ItemStore](ctx)
    settings := muzak.From[core.Settings](ctx)

    stored := store.List()
    items := make([]schemas.ItemOut, 0, len(stored))
    for _, item := range stored {
        items = append(items, schemas.ItemOut{ID: item.ID, Name: item.Name})
    }
    return schemas.ItemListOut{Items: items, Limit: settings.ItemsPerUser}, nil
}
```

::

Constructing the output is more typing than returning the stored record. In exchange, a
column added to that record tomorrow cannot appear in a response, because it is not part of
the response type, and the compiler is what tells you rather than a bug report.

## Nesting

Named structs become named components in the document and are referenced by name, so a type
used by several operations is described once.

```go
type FeedEntry struct {
    ID      string   `json:"id"`
    URL     string   `json:"url"`
    Title   string   `json:"title"`
    Tags    []string `json:"tags"`
    Summary string   `json:"summary,omitzero"`
}

type FeedOut struct {
    Reader    string      `json:"reader"`
    Trimmed   bool        `json:"trimmed"`
    Tracked   bool        `json:"tracked"`
    Entries   []FeedEntry `json:"entries"`
    UpdatedAt time.Time   `json:"updated_at"`
}
```

Embedding promotes members the way JSON encoding does, so a shared group of fields is
declared once and appears inline on every model that embeds it.

## When the body is not JSON

- Return `muzak.HTML` for a page. See
  [Forms and HTML](/docs/techniques/forms-and-html).
- Return `muzak.Empty` with `muzak.Status(http.StatusNoContent)` for no body at all.
- Take `ctx.ResponseWriter()` for a file download or anything else Muzak should not
  encode. See [Responses](/docs/getting-started/responses).
- Reach for [Server-Sent Events](/docs/realtime/server-sent-events) for a body
  that never ends.

## Deterministic output for the document

`Document.Marshal` renders the OpenAPI document with map keys in sorted order, so the
output is byte-for-byte reproducible. That is what lets a generated document be committed
and diffed. Ordinary responses are not sorted; a Go struct already has a field order, and
that order is what goes out.

## Where to go next

[Validation](/docs/fundamentals/validation) covers checking a decoded body, and
[Compression](/docs/techniques/compression) covers making a large one smaller.


--------------------------------------------------------------------------------
title: "Forms and HTML"
description: "Bind a form body into a typed input, and return an HTML document from the same application that serves the API."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/techniques/forms-and-html"
--------------------------------------------------------------------------------

# Forms and HTML

A field tagged `form:"name"` is bound from a form body, converted by the same setters that
convert a query parameter. A route that binds form values and no files at all is not a
special kind of route: it validates, documents and fails exactly like any other.

## Binding a form

```go [schemas/login.go]
// LoginIn is the sign-in form.
//
// The two fields carry no location tag beyond `form`, so this route reads a
// form body rather than JSON and accepts both encodings a browser can produce:
// application/x-www-form-urlencoded, which a plain HTML form posts, and
// multipart/form-data, which one with an enctype does.
type LoginIn struct {
    Username string `form:"username" doc:"The account to sign in to"`
    Password string `form:"password" doc:"The account's password"`

    // Next is where to send the browser afterwards. A form value is required
    // like the body is, so an optional one says so.
    Next string `form:"next" required:"false" doc:"Where to redirect after signing in"`
}
```

A form value is body content, so it is **required by default**, unlike a query parameter.
Mark an optional one `required:"false"` or give it a `default`.

```go
type SearchForm struct {
    Query string `form:"q"`
    Page  int    `form:"page" default:"1"`
    Safe  bool   `form:"safe" default:"true"`
}
```

## What a form route accepts

| The input binds | Accepted media types |
|---|---|
| `form` fields only | `application/x-www-form-urlencoded` and `multipart/form-data` |
| any `file` field | `multipart/form-data` only, because urlencoded cannot carry a file |

That is why a plain HTML form with no `enctype` posts to a form route without being told
to.

An input that binds `form` or `file` fields cannot also declare JSON members. A field with
no location tag on such an input is a build error telling you to tag it with `form` or move
it to the path, query, header or cookie.

## Validating a form

```go [schemas/login.go]
// Validate bounds the credentials before any comparison is attempted.
//
// The rules are deliberately shape-only. A password that is too short is worth
// rejecting outright, but nothing here may hint at whether the account exists;
// that answer belongs to the handler, which gives the same one either way.
func (in *LoginIn) Validate(v *muzak.Validation) {
    v.String(&in.Username).Trim().Lower().MinLen(2).MaxLen(32)
    v.String(&in.Password).MinLen(8).MaxLen(128)
}
```

Failures are reported with `"location": "body"`, because a form value is body content.

## The handler

::code-group

```go [handlers/login.go]
// Login establishes a session from a submitted form.
//
// By the time this runs the form has been read, the username trimmed and
// lower-cased and both lengths checked, so the handler is left with the one
// decision that is actually its own.
func Login(ctx *muzak.Context, in schemas.LoginIn) (schemas.LoginOut, error) {
    expected, known := accounts[in.Username]

    // The comparison runs even for an unknown account, and the same answer is
    // given either way. Returning "no such user" would turn this endpoint into
    // a way to enumerate accounts, and returning early would let its timing do
    // the same thing more quietly.
    matches := subtle.ConstantTimeCompare([]byte(expected), []byte(in.Password)) == 1
    if !known || !matches {
        return schemas.LoginOut{}, muzak.Unauthorized("the username or password is incorrect")
    }

    session := uuid.NewV4().String()
    ctx.SetCookie(&http.Cookie{
        Name:     "session_id",
        Value:    session,
        Path:     "/",
        HttpOnly: true,
        SameSite: http.SameSiteLaxMode,
        Secure:   false,
        MaxAge:   3600,
    })

    return schemas.LoginOut{
        Username:  in.Username,
        SessionID: session,
        Next:      in.Next,
    }, nil
}
```

```go [routers/login.go]
func Auth() *muzak.Router {
    r := muzak.NewRouter(muzak.WithTags("auth"))

    r.Get("/login", handlers.LoginForm,
        muzak.Summary("Serve the sign-in form"))

    r.Post("/login/", handlers.Login,
        muzak.Summary("Exchange a username and password for a session"),
        muzak.WithResponseDoc(http.StatusUnauthorized, "The username or password is incorrect"),
        // Stricter than the rest of the application, because guessing a
        // password is the one request worth making a hundred times a minute.
        muzak.RateLimit(muzak.Quota{Name: "login", Window: time.Minute, Limit: 5}))

    return r
}
```

::

The rate limit on a sign-in route is not optional in practice. See
[Rate Limiting](/docs/techniques/rate-limiting) for why the count belongs before
the guards.

## Returning HTML

A handler returning `muzak.HTML` bypasses JSON encoding entirely: the string is written
verbatim under `text/html`, and the generated document describes the response as
`text/html` rather than as a JSON schema.

```go [handlers/login.go]
// LoginForm serves the page that posts to Login.
//
// It is a plain form with no enctype, so the browser posts it as
// application/x-www-form-urlencoded. The route accepts that without being told
// to, because it binds form values and no files.
func LoginForm(ctx *muzak.Context, _ muzak.Empty) (muzak.HTML, error) {
    return muzak.HTML(`<body>
<form action="/login/" method="post">
<label>Username <input name="username" autocomplete="username"></label>
<label>Password <input name="password" type="password" autocomplete="current-password"></label>
<input type="hidden" name="next" value="/feed">
<input type="submit" value="Sign in">
</form>
</body>`), nil
}
```

Everything else about the route is unchanged: status, headers, cookies and errors work
exactly as they do for a JSON route.

### Escaping

The value is written as given. Muzak does not escape it, because it cannot tell markup the
handler meant from text it did not. Anything a client supplied has to be escaped by the
handler.

```go
func Greeting(ctx *muzak.Context, in GreetIn) (muzak.HTML, error) {
    return muzak.HTML("<p>Hello, " + html.EscapeString(in.Name) + "</p>"), nil
}
```

For anything larger than a fragment, use `html/template`, which escapes by construction:

```go
var page = template.Must(template.ParseFiles("templates/profile.html"))

func Profile(ctx *muzak.Context, in ProfileIn) (muzak.HTML, error) {
    var buf strings.Builder
    if err := page.Execute(&buf, in); err != nil {
        return "", err
    }
    return muzak.HTML(buf.String()), nil
}
```

Parse the templates once and publish the set with `muzak.WithSingleton` or
`muzak.Singleton` rather than parsing per request. See
[Dependencies](/docs/getting-started/dependencies).

## Redirecting after a post

```go
func Login(ctx *muzak.Context, in schemas.LoginIn) (muzak.Empty, error) {
    // ... establish the session ...
    ctx.SetHeader("Location", in.Next)
    ctx.SetStatus(http.StatusSeeOther)
    return muzak.Empty{}, nil
}
```

`Next` came from the client, so treat it as input: validate it against a set of known
destinations, or require it to be a relative path, before putting it in a `Location`
header.

## Body limits

A route that binds `form` or `file` fields is bounded by `MaxUploadSize` rather than
`MaxBodySize`, defaulting to 32 mebibytes. Declare it once on the router that holds those
routes:

```go
r := muzak.NewRouter(
    muzak.WithTags("uploads"),
    muzak.MaxUploadSize(32<<20),
    muzak.MaxFileSize(10<<20),
)
```

## Testing a form route

```go
func TestLogin(t *testing.T) {
    client := testclient.New(t, buildApp())

    form := url.Values{"username": {"muzak"}, "password": {"correct-horse-battery"}}
    res := client.Post("/login/", testclient.Body(
        "application/x-www-form-urlencoded", strings.NewReader(form.Encode())))

    res.AssertStatus(http.StatusOK)
    if len(res.Cookies) == 0 {
        t.Error("want a session cookie")
    }
}
```

## Where to go next

[File Uploads](/docs/techniques/file-uploads) covers the other half of a
multipart body, and [Cookies](/docs/techniques/cookies) covers the session a
sign-in establishes.


--------------------------------------------------------------------------------
title: "File Uploads"
description: "Bind an uploaded file into a typed field, choose between bytes in memory and a handle to the content, and bound what a client can send."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/techniques/file-uploads"
--------------------------------------------------------------------------------

# File Uploads

A field tagged `file:"name"` is bound from the part the client sent under that name, and
its Go type decides what the handler is handed.

| Field type | What the handler gets |
|---|---|
| `[]byte` | The content read straight into memory |
| `muzak.File` | The metadata, with the content left where the parser put it |
| `[]muzak.File` | Every file sent under the name |
| `[][]byte` | The content of every file sent under the name |

```go [schemas/uploads.go]
// FileBytesIn binds one upload straight into memory, which suits a file small
// enough that holding all of it at once is not a decision worth thinking
// about.
type FileBytesIn struct {
    File []byte `file:"file" doc:"A file read as bytes"`
}

// UploadFileIn binds one upload alongside a form value, which is what an HTML
// form with a file input and a text input sends.
type UploadFileIn struct {
    File muzak.File `file:"file" doc:"A file read as an upload"`
    Note string      `form:"note" doc:"An optional note filed with the upload" required:"false"`
}

// MultiUploadIn binds every file sent under one name.
type MultiUploadIn struct {
    Files []muzak.File `file:"files" doc:"One or more files"`
}
```

A file is body content, so it is **required by default**. Mark an optional one
`required:"false"`.

## Handlers

```go [handlers/uploads.go]
// FileSize reports the size of a file read into memory. Binding it as []byte is
// what makes that the whole handler.
func FileSize(ctx *muzak.Context, in schemas.FileBytesIn) (schemas.FileOut, error) {
    return schemas.FileOut{FileSize: len(in.File)}, nil
}

// UploadFile reports what a client sent with one file. Nothing is read here:
// muzak.File carries the metadata and leaves the content where the parser put
// it, so a handler that only needs the name never touches the bytes.
func UploadFile(ctx *muzak.Context, in schemas.UploadFileIn) (schemas.UploadFileOut, error) {
    return schemas.UploadFileOut{
        Filename:    in.File.Filename,
        ContentType: in.File.ContentType,
        Size:        in.File.Size,
        Note:        in.Note,
    }, nil
}

// UploadFiles reports every file sent under one name.
func UploadFiles(ctx *muzak.Context, in schemas.MultiUploadIn) (schemas.MultiUploadOut, error) {
    out := schemas.MultiUploadOut{Filenames: make([]string, len(in.Files))}
    for i, file := range in.Files {
        out.Filenames[i] = file.Filename
        out.TotalSize += file.Size
    }
    return out, nil
}
```

## Working with `muzak.File`

```go
type File struct {
    // Filename is the name the client reported for the file. It is arbitrary
    // client-supplied text and must never be used as a path, a database key or
    // anything else with meaning on the server without being checked first.
    Filename string
    // ContentType is the media type declared for the part, which is likewise
    // what the client claimed rather than what the bytes contain.
    ContentType string
    // Size is the number of bytes the file holds.
    Size int64
}
```

| Method | What it does |
|---|---|
| `Open()` | A reader over the content, positioned at the start. The caller owns and must close it. Opening more than once is allowed, and each reader has its own position |
| `Bytes()` | The whole file in memory, as a fresh slice that stays valid after the request ends |
| `Save(path)` | Copies the file to a path, creating or truncating it, and reports the bytes written |
| `Header()` | The MIME headers of the part, for the occasional client that sends more than a filename and a content type |
| `Present()` | Whether a file was uploaded at all. Only ever false for a field marked `required:"false"` |

`Open` and `Bytes` report `muzak.ErrNoFile` when nothing was uploaded.

```go
func StoreAvatar(ctx *muzak.Context, in AvatarIn) (AvatarOut, error) {
    user := muzak.From[core.CurrentUser](ctx)

    source, err := in.Avatar.Open()
    if err != nil {
        return AvatarOut{}, err
    }
    defer source.Close()

    // The destination is built from a directory the server controls and a name
    // the server generates. in.Avatar.Filename is never part of a path.
    name := uuid.NewV4().String() + extensionFor(in.Avatar.ContentType)
    destination := filepath.Join(storageDir, user.Username, name)

    written, err := in.Avatar.Save(destination)
    if err != nil {
        return AvatarOut{}, err
    }
    return AvatarOut{Name: name, Size: written}, nil
}
```

## Lifetime

A part larger than 10 mebibytes spills to a temporary file while the body is parsed, and
every temporary file is removed once the handler returns. `Open` and `Bytes` are therefore
only valid while the handler runs: anything that must outlive the request has to be copied
out of it first, with `Save` or otherwise.

That threshold is deliberately well below the default upload limit, so a handful of
concurrent large uploads cannot be turned into memory pressure.

## Nothing the client sends is trusted

`Filename` and `ContentType` are text the client chose. A client may send
`../../etc/passwd`, a name that means something to the local filesystem, or a
`Content-Type` that has nothing to do with the bytes behind it.

- Build a destination from a directory the server controls and a name the server generates.
- Sniff the content if the type matters, rather than believing the declaration.
- Echo the filename back if you like, but never route on it.

## Limits

Two limits bound what a route accepts, and both are declared rather than remembered.

```go [routers/uploads.go]
// Uploads returns the router for the endpoints that accept files.
//
// The two limits are declared once for the whole router, so a route added here
// later cannot forget them: MaxUploadSize bounds what the server will read at
// all, and MaxFileSize bounds any single file inside it.
func Uploads() *muzak.Router {
    r := muzak.NewRouter(
        muzak.WithTags("uploads"),
        muzak.MaxUploadSize(32<<20),
        muzak.MaxFileSize(10<<20),
    )

    r.Get("/upload", handlers.UploadForm,
        muzak.Summary("Serve a form that posts files"))

    r.Post("/files/", handlers.FileSize,
        muzak.Summary("Report the size of a file read as bytes"))

    r.Post("/uploadfile/", handlers.UploadFile,
        muzak.Summary("Report what was sent with one file"))

    r.Post("/uploadfiles/", handlers.UploadFiles,
        muzak.Summary("Report what was sent with several files"))

    return r
}
```

| Limit | Applies to | Default | Behaviour when exceeded |
|---|---|---|---|
| `MaxUploadSize` | The whole form body | `DefaultMaxUploadSize`, 32 MiB | `413` while the body is being read, so the server never buffers more than the limit |
| `MaxFileSize` | Any single file in it | unset, so each file is bounded only by the upload limit | `413` before the handler runs |

Both are settable application-wide through `AppOptions.MaxUploadSize` and
`AppOptions.MaxFileSize`, and both are shared options, so a router or a single route can
narrow them. A route that binds `form` or `file` fields uses `MaxUploadSize` in place of
`MaxBodySize`, because an upload is expected to be larger than a JSON document and the two
limits should not have to be traded off against each other.

The refusal names the field rather than the client's filename, which is text the client
chose and would otherwise be echoed straight back:

```json
{
  "error": {
    "code": "payload_too_large",
    "message": "a file uploaded as \"file\" exceeds the 10485760 byte limit for a single file on this route",
    "status": 413
  },
  "request_id": "0611f4b2-2f0a-4b57-9c1a-6e6a2e2f9b31"
}
```

## A form that posts them

```go [handlers/uploads.go]
// UploadForm serves the page that posts to UploadFiles. Returning muzak.HTML
// is what bypasses JSON encoding; the document is written as it stands.
func UploadForm(ctx *muzak.Context, _ muzak.Empty) (muzak.HTML, error) {
    return muzak.HTML(`<body>
<form action="/uploadfiles/" enctype="multipart/form-data" method="post">
<input name="files" type="file" multiple>
<input type="submit">
</form>
</body>`), nil
}
```

A route that binds any file accepts `multipart/form-data` only, which is why the form
above carries an `enctype`. A route binding form values and no files also accepts
`application/x-www-form-urlencoded`. See
[Forms and HTML](/docs/techniques/forms-and-html).

## Validating an upload

Form values alongside a file validate like anything else, and a rule on the file itself is
a `Value` rule.

```go
func (in *UploadFileIn) Validate(v *muzak.Validation) {
    v.String(&in.Note).Trim().MaxLen(280)
    v.Value(&in.File).Must(func(f muzak.File) error {
        switch f.ContentType {
        case "image/png", "image/jpeg", "image/webp":
            return nil
        default:
            return errors.New("must be a PNG, JPEG or WebP image")
        }
    })
}
```

A declared content type is a hint, not proof. Where it matters, read the first bytes and
check them.

## Testing an upload

```go
func TestUploadFile(t *testing.T) {
    client := testclient.New(t, buildApp())

    var body bytes.Buffer
    form := multipart.NewWriter(&body)
    part, err := form.CreateFormFile("file", "notes.txt")
    if err != nil {
        t.Fatalf("CreateFormFile = %v", err)
    }
    if _, err := part.Write([]byte("hello")); err != nil {
        t.Fatalf("Write = %v", err)
    }
    if err := form.Close(); err != nil {
        t.Fatalf("Close = %v", err)
    }

    res := client.Post("/uploadfile/", testclient.Body(form.FormDataContentType(), &body))

    res.AssertStatus(http.StatusOK)
    res.AssertJSON(`{"filename":"notes.txt","content_type":"application/octet-stream","size":5,"note":""}`)
}
```

## Where to go next

[Forms and HTML](/docs/techniques/forms-and-html) covers the rest of a multipart
body, and [Static Files and Frontends](/docs/techniques/static-files) covers
serving files back out.


--------------------------------------------------------------------------------
title: "Static Files and Frontends"
description: "Serve a built frontend and a directory of assets from the same binary as the API, with routes always matched first."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/techniques/static-files"
--------------------------------------------------------------------------------

# Static Files and Frontends

Two mounts, one machinery. `Router.Frontend` serves the output of a frontend build and
resolves a path with no file behind it; `Router.Static` serves a directory and lets a miss
stay a miss.

```go [cmd/main.go]
// Assets that belong to no particular route. A static mount serves what it
// finds and nothing else, so a miss here stays a miss rather than being
// answered with the application document by the frontend below.
app.Static("/static", muzak.StaticOptions{Dir: "static"})

// The built frontend is served last: every route above is matched first, so
// mounting at the root cannot shadow the API.
app.Frontend("/", muzak.FrontendOptions{Dir: "dist"})
```

Nothing is rendered on the server and nothing is built here. Both serve files that already
exist, which is what `npm run build` and its equivalents produce.

## Routes win

A request is matched against every registered route first and reaches a mount only when
none of them answered, so mounting a frontend at `/` cannot shadow an API. Middleware still
applies, and so do the guards of the router the mount was registered on, which is what lets
a frontend sit behind the same authentication as everything else.

```go
ui := muzak.NewRouter(muzak.WithDependencies(core.RequireSession))
ui.Frontend("/", muzak.FrontendOptions{Dir: "dist"})
app.Include(ui)
```

Mounting under a prefix works the way everything else does, through the router the mount is
registered on:

```go
ui := muzak.NewRouter()
ui.Frontend("/", muzak.FrontendOptions{Dir: "dist"})
app.Include(ui, muzak.WithPrefix("/app"))
```

## Serving a frontend

```go
app.Frontend("/", muzak.FrontendOptions{Dir: "dist"})
```

A request for a path with no file behind it falls back to one, resolved from what the build
actually produced:

1. A `404.html` in the frontend's root is served with `404`.
2. Failing that, an `index.html` is served with `200`, but only for a `GET` or `HEAD` that
   asks for HTML, which is what a browser navigation does.

That second rule is what a client-side router needs in order to take over. The restriction
to navigations is what keeps a missing script or stylesheet answering `404`, because
handing those an HTML document only turns a missing file into a confusing parse error
somewhere further from the cause.

| Option | Effect |
|---|---|
| `Dir` | The directory holding the build, or the subdirectory within `FS` when both are set |
| `FS` | Serve from an `io/fs.FS` rather than from disk |
| `Fallback` | Name the file served with `200` for a navigation, instead of resolving it |
| `NotFound` | Name the file served with `404`. It takes precedence over `Fallback` and applies to any `GET` or `HEAD` |
| `NoFallback` | Serve a plain `404` for anything with no file behind it |
| `SkipCheck` | Do not verify the directory when the application is built, for one something else fills in later |

## Shipping the frontend inside the binary

```go [cmd/main.go]
//go:embed all:dist
var assets embed.FS

func main() {
    app := muzak.New(muzak.AppOptions{Title: "Awesome API"})
    app.Include(routers.Items())

    app.Frontend("/", muzak.FrontendOptions{FS: assets, Dir: "dist"})

    log.Fatal(app.RunSignals())
}
```

Setting both reads `Dir` as a subdirectory of `FS`, which is what the directory embed above
produces. The deployment is then one file.

Use `all:dist` rather than `dist`, or the embed skips files whose names begin with `.` or
`_`, which is exactly what several build tools emit.

## Serving assets

```go
app.Static("/static", muzak.StaticOptions{Dir: "static"})
```

`Static` is the same machinery without the part that makes a frontend work: nothing stands
in for a path with no file behind it, so a miss is a `404` and stays one.

| Option | Effect |
|---|---|
| `Dir` | The directory holding the files, or the subdirectory within `FS` |
| `FS` | Serve from an `io/fs.FS`, which is what an `embed.FS` of assets looks like |
| `Index` | Serve a directory with the `index.html` inside it, as a web server does for a site of pages |
| `SkipCheck` | Do not verify the directory when the application is built |

`Index` is off by default, because a mount of scripts and stylesheets has no index and
asking for a directory is a mistake worth reporting.

Reach for `Static` to publish assets, and for `Frontend` to serve an application whose
routing happens in the browser.

## What both refuse

| A request that... | ...gets |
|---|---|
| asks for a directory | never a listing. A `Static` mount answers its `index.html` only when `Index` asked for it |
| follows a symbolic link out of the mounted directory | refused |
| uses a method other than `GET` or `HEAD` on a file that exists | `405`, rather than the file |
| arrives before a `SkipCheck` directory exists | `500`, with the reason logged |

A directory that does not exist is reported when the application is built rather than on
the first request, unless `SkipCheck` asked otherwise.

## A single binary, front to back

```
awesome-api/
├── cmd/
│   └── main.go
├── dist/               what the frontend build wrote
│   ├── index.html
│   ├── 404.html
│   └── assets/
├── static/             assets that belong to no route
│   └── logo.svg
├── routers/
├── handlers/
├── schemas/
└── core/
```

```go [cmd/main.go]
app.Include(routers.Users())
app.Include(routers.Items())

app.Static("/static", muzak.StaticOptions{Dir: "static"})
app.Frontend("/", muzak.FrontendOptions{Dir: "dist"})
```

`GET /users/` reaches the API. `GET /static/logo.svg` reaches the asset. `GET /dashboard`
reaches no route and no file, so `index.html` is served with `200` and the browser's router
takes it from there. `GET /assets/app-4f2a.js` with nothing behind it answers `404`, so a
stale hashed asset fails loudly instead of returning HTML.

## Where to go next

[Compression](/docs/techniques/compression) covers making those assets smaller,
and [Server Configuration](/docs/deployment/server-configuration) covers
deploying the binary that holds them.


--------------------------------------------------------------------------------
title: "Compression"
description: "Negotiate gzip or deflate in one line, leave alone what compressing would not help, and know the one case where compression and secrecy interact badly."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/techniques/compression"
--------------------------------------------------------------------------------

# Compression

```go
app.Use(muzak.Compress(muzak.CompressionOptions{}))
```

That is the whole installation. The middleware negotiates an encoding from
`Accept-Encoding`, compresses the bodies worth compressing, and records `Vary` on every
response either way.

Install it with `App.Use`, which puts it inside the built-in chain, so it already has a
request identifier and is already covered by panic recovery.

## What is negotiated

gzip is preferred over deflate, and an explicit refusal such as `gzip;q=0` is honoured. A
client that asks for neither gets an uncompressed body.

`Vary: Accept-Encoding` is set on every response whether or not it was compressed, so a
cache cannot hand a compressed body to a client that cannot read it.

## What is left alone

| A response that... | ...is not compressed |
|---|---|
| is smaller than `MinSize` | a body that already fits in one packet cannot arrive sooner by shrinking |
| carries a media type that is not text-like | an image, a video or an archive is already compressed |
| the handler encoded itself | a `Content-Encoding` is already set |
| carries no body | there is nothing to compress |
| is a range | a compressed range is not the range that was asked for |
| is an event stream | holding events in a compressor's window until something forces them out is the one thing a stream cannot survive |

`muzak.DefaultCompressionMinSize` is 1400 bytes, a little under one ethernet MTU. A
response that already fits in a single packet cannot be made to arrive sooner by shrinking
it, and compressing it spends CPU on both ends to save nothing.

## Options

```go
app.Use(muzak.Compress(muzak.CompressionOptions{
    Level:        muzak.CompressionBest,
    MinSize:      2048,
    ContentTypes: []string{"text/", "application/json", "+json", "image/svg+xml"},
}))
```

| Field | Default | Effect |
|---|---|---|
| `Level` | `CompressionDefault` | How hard the compressor works |
| `MinSize` | `DefaultCompressionMinSize` | The smallest body worth compressing. A response whose length is not known in advance is buffered up to this size before the decision is made |
| `ContentTypes` | a built-in list of text-like types | Which media types are compressed |

| Level | For |
|---|---|
| `CompressionDefault` | Balances speed against size, which is the right choice until a measurement says otherwise |
| `CompressionFastest` | A service that is CPU bound, or serving large bodies to a fast network |
| `CompressionBest` | A service whose clients are on slow or metered connections |

There are three levels rather than an integer because the underlying range has invalid
values in it and there is nothing useful to do with a level of 42 at run time.

An entry in `ContentTypes` is matched as a prefix, so `text/` covers every text type, and
an entry beginning with `+` matches a structured syntax suffix, so `+json` covers
`application/problem+json`.

## What it is worth

On the framework's own example application, compression takes 91% off the OpenAPI document
and 68% off the documentation page. A JSON listing of any size behaves much the same. A
small response, an image and an event stream are all untouched, which is the point of the
exclusions above.

## Compression and secrecy

Compression and secrecy interact badly, and it is worth knowing where.

When a response mixes a secret with something the client controls, its compressed length
leaks how much the two have in common. That is what the BREACH attack recovers a token
from, over many requests. Muzak's own responses do not mix the two, but a handler that
reflects a query parameter back alongside a CSRF token does.

Where that is possible, either leave compression off for the route or stop reflecting the
input.

## Ordering with your own middleware

Middleware installed first is outermost, so a middleware that reports a duration sees the
time spent compressing only if it is installed before the compressor.

```go [cmd/main.go]
// ProcessTime is outermost of the two, so the duration it reports includes the
// time spent compressing.
app.Use(core.ProcessTime())
app.Use(muzak.Compress(muzak.CompressionOptions{}))
```

Anything that wraps the response writer needs to implement `Unwrap() http.ResponseWriter`,
or it will break flushing and hijacking for everything inside it. See
[Middleware](/docs/getting-started/middleware).

## Checking it

```bash
curl -s -H 'Accept-Encoding: gzip' -o /dev/null -D - http://localhost:8080/openapi.json
```

```
HTTP/1.1 200 OK
Content-Type: application/json
Content-Encoding: gzip
Vary: Accept-Encoding
```

```go
func TestOpenAPIIsCompressed(t *testing.T) {
    client := testclient.New(t, buildApp())

    res := client.Get("/openapi.json", testclient.Header("Accept-Encoding", "gzip"))

    res.AssertStatus(http.StatusOK)
    res.AssertHeader("Vary", "Accept-Encoding")
}
```

## Where to go next

[Static Files and Frontends](/docs/techniques/static-files) covers the assets
this makes smaller, and
[Server-Sent Events](/docs/realtime/server-sent-events) covers the one response
kind compression skips on purpose.


--------------------------------------------------------------------------------
title: "Rate Limiting"
description: "A policy of several quotas counted together, a storage you choose, a tracker that decides whose budget is spent, and defaults that fail closed."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/techniques/rate-limiting"
--------------------------------------------------------------------------------

# Rate Limiting

Rate limiting is built in and off until a policy names a quota. There is no limit that is
right for every application, and a default one would be a number nobody chose refusing
traffic nobody expected.

What is safe by default is what happens once a policy exists: the counters are bounded, the
address is not taken from a header anyone can write, and a storage that stops answering
stops traffic rather than stopping the limit.

## A policy is several quotas

```go
type Quota struct {
    Name   string        // the namespace its counters live under, reported to clients
    Window time.Duration // how long one counting period lasts
    Limit  int           // how many requests are allowed within one window
}
```

One number cannot tell a burst from sustained abuse. Three requests a second is generous
for a person clicking and impossible for a script; a hundred a minute is the reverse. A
policy that means "quick but not tireless" needs both.

::code-group

```go [core/ratelimit.go]
// RateLimitPolicy is the application-wide budget every route inherits.
//
// Three windows rather than one, because a single number cannot tell a person
// clicking from a script that never stops. Every quota is counted for every
// request, so a client that overruns the short window still accrues against the
// long one.
func RateLimitPolicy() muzak.RateLimitOptions {
    return muzak.RateLimitOptions{
        Tracker: UserOrIPTracker,
        Quotas: []muzak.Quota{
            {Name: "short", Window: time.Second, Limit: 3},
            {Name: "medium", Window: 10 * time.Second, Limit: 20},
            {Name: "long", Window: time.Minute, Limit: 100},
        },
    }
}
```

```go [cmd/main.go]
app := muzak.New(muzak.AppOptions{Title: settings.AppName, Addr: settings.Addr},
    muzak.WithDependencies(core.GetQueryToken),
    // The application-wide budget, counted before the guard above runs so that
    // a request the guard rejects still costs the client something.
    muzak.WithRateLimit(core.RateLimitPolicy()),
)
```

::

Every quota is counted for every request, so pausing between bursts launders nothing.

A quota's `Name` is the namespace its counters are stored under, so two quotas that share a
name share a budget and must agree on their window and limit. The application refuses to
build when they do not. It must be a valid HTTP token, because it is reported to clients.

## Narrowing it

```go
// A health check: exempt. A monitor polling every second is the one client that
// should never be told to slow down.
r.Get("/healthz", handlers.Health, muzak.SkipRateLimit())

// A login route: stricter. Guessing a password is the one request worth making
// a hundred times a minute.
r.Post("/login/", handlers.Login,
    muzak.RateLimit(muzak.Quota{Name: "login", Window: time.Minute, Limit: 5}))
```

`RateLimit` replaces the quotas and keeps the storage and tracker it inherits, which is the
common case. `WithRateLimit` layers the whole options struct field by field on top of what
an enclosing scope declared, so a route can change one thing without restating the rest.

The quotas given **replace** those inherited rather than adding to them, so a route that
wants both restates the ones it is keeping.

An exemption cannot be undone by a narrower scope: once a router is exempt, every route
beneath it is.

## When the count happens

By default, **before** the route's guards and dependencies. That is the half that matters
for brute force: a request a guard rejects is still counted, so failed sign-ins cost the
attacker their budget rather than being free. It also means a client past its limit is
refused before anything expensive runs on its behalf.

`AfterDependencies` moves the count after them, which is what a tracker keying on a
resolved identity needs, and gives the other half up.

```go [routers/items.go]
r := muzak.NewRouter(muzak.WithTags("items"),
    // This is the one router whose routes resolve a caller, so it is the one
    // router where the budget is worth spending per caller rather than per
    // address. Deferring the count is what lets the tracker see the resolved
    // user; the quotas themselves are inherited unchanged.
    muzak.WithRateLimit(muzak.RateLimitOptions{AfterDependencies: true}))
```

Leave it off on a login route. A request rejected by a guard never reaches a limiter that
defers, so a route that defers does not limit failed authentication at all, which is
exactly the traffic a login route needs to limit.

## Limits that vary per caller

A `Quota` is fixed when the application is built, and the same name may not carry two
policies anywhere in it. That is right for a policy the application owns, and it cannot
express one its customers do: a plan, a negotiated ceiling, a tier read from a database.

`Resolver` supplies quotas per request.

```go [core/ratelimit.go]
// The plan was resolved by a dependency, so the count has to happen after
// them. It is read from whatever the dependency already fetched rather than
// from the database, because this runs on every request.
func PlanQuotas(ctx *muzak.Context) ([]muzak.Quota, error) {
    plan, ok := muzak.TryFrom[Plan](ctx)
    if !ok {
        // Nothing resolved, nothing enforced. An unauthenticated request
        // reaching this route is limited by the static quotas, if any.
        return nil, nil
    }
    return plan.Quotas, nil
}
```

```go [cmd/main.go]
muzak.WithRateLimit(muzak.RateLimitOptions{
    AfterDependencies: true,
    Tracker:           core.UserOrIPTracker,
    Resolver:          core.PlanQuotas,
    // A floor everyone shares, whatever their plan says.
    Quotas: []muzak.Quota{{Name: "burst", Window: time.Second, Limit: 20}},
})
```

Resolved quotas are counted **alongside** the static ones, under their own names, in the
same storage. A route can therefore carry both a floor everyone shares and a ceiling that
varies, and `RateLimit-Policy` describes the union.

Setting a resolver turns limiting on for the route even with no static quota, because
what is enforced becomes a run time question. Returning nothing enforces nothing.

Two things the framework cannot check for you here. A quota built at run time is not
validated when the application is built, so one with no name, no window or no limit fails
the request rather than being counted under an empty name. And nothing compares a
resolver's names against the ones declared statically, so a name used in both places must
mean the same thing in both, or one counter serves two policies.

## Whose budget is spent

`RateLimitTracker` is `func(ctx *muzak.Context) (string, error)`. It defaults to
`muzak.IPTracker`, which keys on the address `Context.ClientIP` resolves.

```go [core/ratelimit.go]
// UserOrIPTracker spends a request from the caller's budget when there is a
// caller, and from the address's otherwise.
//
// The identity is read with TryFrom rather than From, because most routes here
// resolve no user at all and an absent dependency is a legitimate state for
// this tracker rather than a programming error. Keys from the two sources are
// prefixed differently so that a username can never collide with an address.
func UserOrIPTracker(ctx *muzak.Context) (string, error) {
    if user, ok := muzak.TryFrom[CurrentUser](ctx); ok {
        return "user:" + user.Username, nil
    }
    return muzak.IPTracker(ctx)
}
```

A tracker that requires a credential can insist on one. Returning an error abandons the
request, and the error becomes the response exactly as one returned from a handler would:

```go
func APIKeyTracker(ctx *muzak.Context) (string, error) {
    key := ctx.Header("X-API-Key")
    if key == "" {
        return "", muzak.Unauthorized("an API key is required")
    }
    return "apikey:" + key, nil
}
```

Two rules: the key must not be empty, and keys from different sources must be prefixed
differently, so that a user identifier and an address can never collide into one budget.

### Addresses that are cheap to change

`IPTracker` keys on the exact address, which stops fitting as soon as an address family is
cheap to change. An IPv6 `/64` is the block size most providers hand out, so a client
holding one can present a different address on every request while never leaving a range
only they hold, and each address is a fresh budget.

```go
muzak.RateLimitOptions{Tracker: muzak.IPPrefixTracker(32, 64)}
```

That keeps IPv4 addresses exact while collapsing an IPv6 source down to the allocation it
actually came from. It panics if either length is out of range for its family, which is a
mistake worth catching where the tracker is built rather than on the first request.

The address itself is only as trustworthy as the deployment makes it. See
[Behind a Proxy](/docs/deployment/behind-a-proxy).

## Where the counters live

```go
type RateLimitStorage interface {
    Increment(ctx context.Context, quota, key string, window time.Duration) (count int, reset time.Duration, err error)
}
```

That is the whole of what the limiter needs from the outside world.

### The default, in memory

An application that names no storage gets one that counts in the process that serves the
requests. It is the right answer for a single process and the wrong answer for several:
counters held in one process are not shared with the next, so a limit of a hundred a minute
becomes a hundred a minute per process.

The table is bounded in two directions, because it is keyed by something the client
influences and an unbounded one would be a memory leak with a name. Expired counters are
swept, a full table discards the counter closest to expiring, and a tracker key the client
chose the length of is hashed rather than truncated, so two clients cannot be merged into
one budget.

```go
muzak.WithRateLimit(muzak.RateLimitOptions{
    Storage: muzak.NewMemoryRateLimitStorage(muzak.MemoryRateLimitOptions{
        MaxEntries:    10_000,
        SweepInterval: 30 * time.Second,
    }),
    Quotas: []muzak.Quota{{Name: "default", Window: time.Minute, Limit: 60}},
})
```

`DefaultRateLimitMaxEntries` is 100000 and `DefaultRateLimitSweepInterval` is one minute.
Naming a storage explicitly is only necessary to change those bounds.

It implements `muzak.Lifecycle`, so it is started and stopped with the application, and
stopping releases every counter it holds: a key is derived from whatever the tracker read,
an address, a user identifier or an API key, and none of that should outlive the server
that was counting it.

`Len()` reports how many counters it holds, which is what a metric or a test asking whether
anything is accumulating wants.

### A shared storage

Anything running more than once wants counters the processes share. The interface is three
arguments wide so that whatever you already run can satisfy it.

```go [core/redisratelimit.go]
// Increment counts one request against a quota for one client.
//
// The increment and the expiry happen in one round trip, so that two requests
// arriving together cannot both create the window.
func (s *RedisRateLimitStorage) Increment(
    ctx context.Context, quota, key string, window time.Duration,
) (int, time.Duration, error) {
    // The script runs INCR, then PEXPIRE when the counter is new, then PTTL.
    res, err := s.script.Run(ctx, s.client,
        []string{"ratelimit:" + quota + ":" + key},
        window.Milliseconds()).Result()
    if err != nil {
        return 0, 0, err
    }
    values := res.([]any)
    count := int(values[0].(int64))
    reset := time.Duration(values[1].(int64)) * time.Millisecond
    return count, reset, nil
}
```

```go
muzak.WithRateLimit(muzak.RateLimitOptions{
    Storage: core.NewRedisRateLimitStorage(settings.RedisAddr),
    Quotas:  core.RateLimitPolicy().Quotas,
})
```

Two rules an implementation must follow:

- The count includes the request being counted, so the first request in a window returns
  one.
- The window is how long a **newly created** counter should live. Never extend the life of
  a counter that already exists, because a limit whose window restarts on every request is
  a limit that never resets.

The key is opaque and may contain any bytes. It is derived from client-supplied data and
must never be logged, because it routinely carries an API key or a user identifier.

If the implementation also satisfies `muzak.Lifecycle`, the application starts it before
serving and stops it after draining, so a pool or a sweeper needs no separate registration.

## When the storage cannot answer

By default the request is refused with `503`, because a limiter that cannot count is a
limiter that is not enforcing anything, and an attacker who can reach the storage can
choose the moment it stops answering.

```go
muzak.RateLimitOptions{FailOpen: true}
```

That trades the guarantee for availability: a storage outage lets traffic through unmetered
instead of turning into an outage of its own. The failure is logged either way, without the
key.

## What a client sees

Every counted request carries headers describing the budget, because a client that can see
its own budget is a client that can stay inside it.

| Header | Reports |
|---|---|
| `RateLimit-Limit` | The quota closest to being spent |
| `RateLimit-Remaining` | How many requests are left in that quota's current window |
| `RateLimit-Reset` | How many seconds until that window starts again |
| `RateLimit-Policy` | Every quota the route enforces, as `limit;w=seconds` entries |
| `Retry-After` | On a refusal, how long to wait, in seconds |

`RateLimit-Policy` is fixed for a route, so one response teaches a client the whole policy.

```
HTTP/1.1 429 Too Many Requests
RateLimit-Limit: 3
RateLimit-Remaining: 0
RateLimit-Reset: 1
RateLimit-Policy: 3;w=1, 20;w=10, 100;w=60
Retry-After: 1
```

```json
{
  "error": {
    "code": "too_many_requests",
    "message": "the \"short\" rate limit of 3 requests per 1 seconds has been exceeded; retry in 1 seconds",
    "status": 429
  },
  "request_id": "0611f4b2-2f0a-4b57-9c1a-6e6a2e2f9b31"
}
```

`Retry-After` is set even when `DisableHeaders` turns the `RateLimit` headers off. Refusing
a client without telling it when to come back is what produces a client that comes back
immediately, forever.

## Limiting a connected peer

`ReadLimit` bounds what one WebSocket message costs and `MaxConnections` bounds how many
peers there are, but neither bounds a peer that stays inside both and simply never pauses.
`WSOptions.MessageLimits` applies the same quotas, storage and tracker to messages.

```go
r.WS("/items/{item_id}/ws", handlers.ItemSocket,
    muzak.WithWebSocket(muzak.WSOptions{
        MessageLimits: []muzak.Quota{
            {Name: "ws-messages", Window: time.Second, Limit: 10},
        },
    }))
```

A peer that goes over is closed with `1008 Policy Violation` rather than left connected and
ignored, because a message silently dropped is a protocol nobody can debug. The budget
belongs to the client rather than the connection, so opening a second one does not buy a
second budget.

Give those quotas names of their own unless a shared budget with the HTTP routes is what
you want, since both live in one namespace. A route marked `SkipRateLimit` counts no
messages either.

## Options reference

| Field | Default | Effect |
|---|---|---|
| `Quotas` | none, so nothing is limited | The limits enforced, all of them, for every request |
| `Storage` | a bounded in-process table | Where the counters live |
| `Tracker` | `IPTracker` | Whose budget a request is spent from |
| `Resolver` | none | Supplies quotas per request, counted alongside `Quotas` |
| `FailOpen` | off | Serve a request the storage could not count |
| `DisableHeaders` | off | Stop setting the `RateLimit` headers |
| `AfterDependencies` | off | Count after the guards and providers rather than before |

## Where to go next

[Behind a Proxy](/docs/deployment/behind-a-proxy) covers which address a request
is attributed to, and [WebSockets](/docs/realtime/websockets) covers the other
bounds a connected peer runs into.


--------------------------------------------------------------------------------
title: "Response Models"
description: "The handler's return type describes the success response. Every other status code a route answers is declared at registration, with a schema of its own."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/techniques/response-models"
--------------------------------------------------------------------------------

# Response Models

A handler has one return type, and that type describes one outcome: the one where nothing
went wrong. A route that answers `201` with the thing it just created, `400` with a
complaint about the request and `409` with a conflict report is describing three different
shapes, and only the first of them is in the signature.

`Status`, `WithResponseDoc` and `WithResponseModel` are how the other two reach the
document. None of them changes anything at run time. The success body is still enforced by
the compiler; these describe what the route already does for everything else.

## The status a success is written with

```go
r.Post("/users/", handlers.CreateUser, muzak.Status(http.StatusCreated))
```

`Status` is the code written when the handler returns without an error. It defaults to
`200`, and it is part of the route because it never varies: a creating route creates every
time it succeeds.

A status that depends on what happened is decided in the handler instead.

```go
func CreateItem(ctx *muzak.Context, in schemas.ItemCreateIn) (schemas.ItemOut, error) {
    if in.Async {
        ctx.SetStatus(http.StatusAccepted)
    }
    return schemas.ItemOut{ID: in.ID}, nil
}
```

The two never compete: `SetStatus` wins when both are used, and the document describes the
declared one. Declaring the run-time alternative is what the rest of this page is for.

## A schema per status code

`WithResponseModel[T]` documents one status code and the model its body carries. The type
argument is written exactly as a handler's `Out` type would be.

::code-group

```go [schemas/users.go]
type UserOut struct {
    ID       int    `json:"id"`
    Username string `json:"username" doc:"The name the account signs in with"`
}

// ErrorOut is this service's own error body, which is not Muzak's envelope.
type ErrorOut struct {
    Message string `json:"message" doc:"What went wrong"`
    Code    string `json:"code" doc:"A machine-readable classifier"`
}
```

```go [routers/users.go]
r.Post("/users/", handlers.CreateUser,
    muzak.Status(http.StatusCreated),
    muzak.WithResponseModel[schemas.ErrorOut](http.StatusBadRequest, "The request was malformed"),
    muzak.WithResponseModel[schemas.ErrorOut](http.StatusInternalServerError, ""),
)
```

::

Each model is described once in the components section and referenced from every operation
that names it, with the same `doc` tags, the same treatment of embedded structs and
pointers, and the same well-known types a return type gets. Nothing about it is a second
class of schema.

An empty description falls back to the status code's standard reason phrase, so the last
line above reads as *Internal Server Error* in the document and in any page rendering it.

## When the envelope is already right

A handler that reports a failure by returning an error produces Muzak's own error
response. There is no model to choose, only an outcome to name.

```go
r.Get("/items/{item_id}", handlers.ReadItem,
    muzak.WithResponseDoc(http.StatusNotFound, "The item does not exist"))
```

`WithResponseDoc` documents that status as `ErrorResponse`, the shape
[Error Handling](/docs/getting-started/error-handling) covers, because that is what the
route actually answers with. Reach for `WithResponseModel` when the body is something
else: a legacy error shape kept for older clients, a partial result, or a body the handler
writes itself.

Its description may be left empty too, so `WithResponseDoc(http.StatusNotFound, "")` is
documented as *Not Found*.

## No body, and bodies that are not JSON

A response model follows the same rules as a return type, including the two types that are
not JSON at all.

| Model | What is documented |
|---|---|
| a named struct | `application/json`, referencing the component |
| `muzak.Empty` | the status code, with no content |
| `muzak.HTML` | `text/html` carrying a string |

```go
r.Get("/feed", handlers.Feed,
    muzak.WithResponseModel[muzak.Empty](http.StatusNotModified, "The feed has not changed"))
```

## Declaring one for a whole router

`WithResponseModel` and `WithResponseDoc` are both router options as well as route
options, so an outcome every route shares is declared once.

```go
api := muzak.NewRouter(
    muzak.WithResponseModel[schemas.ErrorOut](http.StatusUnauthorized, "No usable credential was presented"))

api.Get("/items/{item_id}", handlers.ReadItem,
    muzak.WithResponseModel[schemas.ItemGoneOut](http.StatusGone, "The item was deleted"))
```

Every route beneath the router carries the `401`, and the same works at the point of
inclusion: `app.Include(api, muzak.WithResponseModel[schemas.ErrorOut](418, "I'm a teapot"))`.

Declarations are applied outermost first, and the last one for a status code wins, so a
route replaces what it inherited by declaring the same code again. That also means a
declaration naming the status the route *succeeds* with replaces the response derived from
the return type, which is deliberate for a route that writes its own body and a mistake
otherwise.

A code outside `100` to `599` is not a status code, and fails the build rather than
becoming a response nobody could receive.

## What ends up in the document

For the creating route above:

```json
"responses": {
  "201": {
    "description": "Created",
    "content": {"application/json": {"schema": {"$ref": "#/components/schemas/UserOut"}}}
  },
  "400": {
    "description": "The request was malformed",
    "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorOut"}}}
  },
  "422": {
    "description": "The request could not be validated.",
    "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorResponse"}}}
  },
  "500": {
    "description": "Internal Server Error",
    "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorOut"}}}
  },
  "default": {
    "description": "An unexpected error occurred.",
    "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorResponse"}}}
  }
}
```

The `422` is added for you on every route that binds anything, since a request that fails
validation never reaches the handler, and a `default` carrying the error envelope closes
the list. Declaring `422` yourself replaces it, like any other code.

The [documentation dashboard](/docs/fundamentals/openapi), if the application serves one,
renders each of them as its own panel, with an example and an expandable schema per media
type, so a client author can read the failure shapes without leaving the page.

## Keep it honest

All of this is description. Nothing verifies at run time that a `404` really carries the
model declared for it, so a declaration is a promise the handler has to keep. Document the
outcomes the route already produces, and let the return type keep speaking for the one the
compiler can check.

## Where to go next

[Responses](/docs/getting-started/responses) covers the return type itself, headers,
cookies and writing the response yourself.
[Error Handling](/docs/getting-started/error-handling) covers the envelope
`WithResponseDoc` describes, and [OpenAPI](/docs/fundamentals/openapi) covers the rest of
the generated document.


--------------------------------------------------------------------------------
title: "WebSockets"
description: "A WebSocket route registered like any other, with the input bound and the guards run before a single byte is upgraded, and every direction a peer controls bounded."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/realtime/websockets"
--------------------------------------------------------------------------------

# WebSockets

`Router.WS` registers a WebSocket route. The protocol is implemented in the framework
rather than delegated to a library: RFC 6455 framing, masking, UTF-8 validation and the
close handshake, with every rule the specification lays down enforced and every violation
answered with the status it calls for.

::code-group

```go [routers/items.go]
// A WebSocket route is declared like any other: the input is bound from the
// handshake, the dependency resolves before the upgrade, and the handler owns
// the connection until it returns.
r.WS("/items/{item_id}/ws", handlers.ItemSocket,
    muzak.Summary("Talk to an item over a WebSocket"),
    muzak.Needs(core.GetSessionOrToken),
    muzak.WithWebSocket(muzak.WSOptions{
        ReadLimit:    64 << 10,
        PingInterval: 30 * time.Second,
        MessageLimits: []muzak.Quota{
            {Name: "ws-messages", Window: time.Second, Limit: 10},
        },
    }))
```

```go [schemas/items.go]
// WSItemIn is the input of the item's WebSocket route.
//
// A handshake carries no body, so every field comes from the path, the query
// string, a header or a cookie. The pointer makes the query parameter optional:
// it stays nil when the client did not send one, which is how an absent value is
// told from a zero one.
type WSItemIn struct {
    ItemID string `path:"item_id" doc:"The item being talked about"`
    Q      *int   `query:"q" doc:"An optional number echoed back with each reply"`
}
```

```go [handlers/items.go]
// ItemSocket answers a WebSocket conversation about one item.
//
// The handshake has already succeeded by the time this runs: the input is
// bound, the guards have passed and the dependency is resolved, so what is left
// is the conversation itself. Returning ends it, and Muzak closes the
// connection; returning nil closes it normally.
func ItemSocket(ctx *muzak.Context, in schemas.WSItemIn, conn *muzak.WSConn) error {
    session := muzak.From[core.SessionOrToken](ctx)

    for {
        message, err := conn.ReadText(ctx.Context())
        if err != nil {
            // The peer closed, or the connection was lost. Either way there is
            // nothing left to say.
            return nil
        }
        if err := conn.WriteText(ctx.Context(), "credential: "+session.Value); err != nil {
            return err
        }
        if in.Q != nil {
            if err := conn.WriteText(ctx.Context(), fmt.Sprintf("q is %d", *in.Q)); err != nil {
                return err
            }
        }
        if err := conn.WriteText(ctx.Context(),
            fmt.Sprintf("you said %q, about item %s", message, in.ItemID)); err != nil {
            return err
        }
    }
}
```

::

## The handshake is an ordinary GET

Everything that applies to a route applies here. Middleware runs, guards run, dependencies
resolve, and the input struct is bound and validated **before a single byte is upgraded**.

A request that fails any of that is answered with the usual JSON error and never becomes a
connection at all, which is the difference between a rejection a client can read and a
socket that closes a moment after it opened.

```bash
curl -i 'http://localhost:8080/items/plumbus/ws'
```

```json
{
  "error": {
    "code": "unauthorized",
    "message": "a session cookie or a token query parameter is required",
    "status": 401
  },
  "request_id": "0611f4b2-2f0a-4b57-9c1a-6e6a2e2f9b31"
}
```

Because a handshake carries no body, an input type with a body field is a registration
error rather than a request that mysteriously never arrives.

### Where a browser can put a credential

A browser cannot set headers on a WebSocket handshake, so the two places a credential can
arrive are a cookie the browser attaches itself and a query parameter the page puts in the
URL. A dependency covering both is what makes the route usable from either.

```go [core/dependencies.go]
func GetSessionOrToken(ctx *muzak.Context) (SessionOrToken, error) {
    if cookie, err := ctx.Cookie("session"); err == nil && cookie.Value != "" {
        return SessionOrToken{Value: cookie.Value, FromCookie: true}, nil
    }
    if token := ctx.Query("token"); token != "" {
        return SessionOrToken{Value: token}, nil
    }
    return SessionOrToken{}, muzak.Unauthorized("a session cookie or a token query parameter is required")
}
```

A cookie is ambient authority a browser attaches without being asked, which is exactly why
the origin check below cannot be turned off casually.

## Reading and writing

Reading and writing are message oriented. A message split across several frames is
reassembled and delivered once, whole. Control frames that arrive in between are handled
without interrupting it: a ping is answered automatically, and a close is answered and then
reported as a `*WSCloseError`.

| Call | What it does |
|---|---|
| `Read(ctx)` | The next message, as a type and a payload |
| `ReadText(ctx)` | The next message as text. A binary message is refused with `1003` |
| `ReadBinary(ctx)` | The next message as bytes. A text message is refused with `1003` |
| `ReadJSON(ctx, &v)` | Decodes a text message with the same strictness a request body gets |
| `Write(ctx, typ, payload)` | One message, as a single frame |
| `WriteText(ctx, s)` | A text message. Invalid UTF-8 is refused before anything reaches the wire |
| `WriteBinary(ctx, b)` | A binary message |
| `WriteJSON(ctx, v)` | Encodes as JSON and sends it as text |
| `Ping(ctx)` | Sends a ping and returns once it is on the wire |
| `Close(status, reason)` | Closes with a status and a reason |
| `Subprotocol()` | The subprotocol negotiated during the handshake, or `""` |

```go
type Command struct {
    Action string `json:"action"`
    Target string `json:"target"`
}

func Control(ctx *muzak.Context, in ControlIn, conn *muzak.WSConn) error {
    for {
        var command Command
        if err := conn.ReadJSON(ctx.Context(), &command); err != nil {
            return nil
        }
        if err := conn.WriteJSON(ctx.Context(), handle(command)); err != nil {
            return err
        }
    }
}
```

`ReadJSON` decodes with the same rules a request body is decoded with: unknown members,
duplicate members and invalid UTF-8 are all rejected. A message that does not decode closes
the connection with `1007`, because a peer sending malformed JSON on a JSON connection is
not going to be understood by carrying on.

The slice `Read` returns belongs to the caller and is not reused, so it may be kept. The
payload passed to `Write` is not retained, so the caller may reuse it as soon as `Write`
returns.

### Concurrency

Writes are serialized, so any number of goroutines may write to one connection and each
message goes out intact. A broadcast from many goroutines cannot interleave two messages.

Reads are serialized too, but a second reader is rarely what is wanted: the messages of one
connection arrive in order and one loop should consume them.

```go
func Room(ctx *muzak.Context, in RoomIn, conn *muzak.WSConn) error {
    hub := muzak.From[*core.Hub](ctx)

    updates := hub.Subscribe(ctx.Context(), in.Room)
    go func() {
        for update := range updates {
            // Safe from another goroutine: writes are serialized.
            _ = conn.WriteJSON(ctx.Context(), update)
        }
    }()

    for {
        message, err := conn.ReadText(ctx.Context())
        if err != nil {
            return nil
        }
        hub.Publish(in.Room, message)
    }
}
```

## Ending a connection

The connection is closed when the handler returns, so a handler owns its connection for as
long as it runs and never has to arrange the teardown itself.

| The handler returns | The peer gets |
|---|---|
| `nil` | `1000 Normal Closure` |
| a `*WSCloseError` | the status and reason it carries |
| anything else | `1011 Internal Error`, with the reason logged and nothing disclosed |

```go
if !allowed(session, in.Room) {
    return &muzak.WSCloseError{Status: muzak.WSStatusPolicyViolation, Reason: "not a member of this room"}
}
```

Calling `Close` yourself is only necessary to choose a status other than normal closure or
to end the connection from another goroutine. It is safe to call more than once and from
more than one goroutine; only the first call sends anything. The reason is truncated to the
123 bytes a close frame can carry, on a rune boundary.

### Telling a goodbye from a violation

Every read and every write returns a `*WSCloseError` once the connection is finished, which
is why the loops above end on any error.

```go
message, err := conn.ReadText(ctx.Context())
if err != nil {
    if status, ok := muzak.WSCloseStatus(err); ok && status == muzak.WSStatusNormalClosure {
        return nil
    }
    return err
}
```

`WSCloseError.Unwrap` exposes the transport failure behind an abnormal closure, so
`errors.Is` can test for `io.EOF` or a network error.

The codes RFC 6455 defines are exported as `muzak.WSStatus` constants, from
`WSStatusNormalClosure` (1000) to `WSStatusTLSHandshake` (1015). Three of them describe a
local observation rather than something a peer said: `WSStatusNoStatusReceived`,
`WSStatusAbnormalClosure` and `WSStatusTLSHandshake` are reported by Muzak but never
written to the wire, and passing one to `Close` closes without a status code. Codes from
4000 to 4999 are free for an application to define.

## What a hostile peer cannot do

A WebSocket is the longest-lived thing an unauthenticated stranger can ask a server for, so
every direction a peer controls is bounded, and each bound stops something specific.

| A peer that... | ...is stopped by |
|---|---|
| sends a message larger than the limit | refused with `1009` before any of the payload is buffered |
| declares a huge payload and sends none of it | a frame is taken a chunk at a time, so six bytes of header cannot buy an allocation the size of the limit |
| dribbles a message out a byte at a time | closed once `ReadTimeout` passes with the message unfinished. Waiting *between* messages stays unbounded |
| fragments a message endlessly, or floods pings | closed once too many frames arrive without one completing, which no size limit would ever catch |
| stops reading what it asked for | writes give up after `WriteTimeout` rather than pinning a goroutine |
| opens connections without end | `MaxConnections` per application, then `503` with `Retry-After` |
| opens many from one address | `MaxConnectionsPerIP`, so one client cannot take every slot the process has |
| opens one from another origin | refused outright |
| sends a body with the handshake | refused, because what went unread would be taken for frames the moment it was upgraded |
| asks for an extension | none is negotiated, so no peer can make the server hold decompression state |
| never pauses, within every limit above | `MessageLimits`, closed with `1008` |

Nothing a peer sends is echoed into a response header: only a subprotocol the route itself
offered can be answered with, and a route offering one that is not a token is refused when
the application is built.

## Options

```go
app.Include(chat, muzak.WithWebSocket(muzak.WSOptions{
    ReadLimit:      64 << 10,
    PingInterval:   30 * time.Second,
    AllowedOrigins: []string{"https://app.example.com"},
}))
```

| Field | Default | Bounds |
|---|---|---|
| `ReadLimit` | 1 MiB | The largest message accepted. There is deliberately no way to remove it |
| `ReadTimeout` | 30s | How long one message may take to arrive once it has begun |
| `WriteTimeout` | 10s | How long one message may take to reach the peer |
| `MaxConnections` | 1024 | Connections the application holds at once. Application-level only |
| `MaxConnectionsPerIP` | 64 | Connections one address holds at once. Application-level only |
| `CloseGracePeriod` | 250ms | How long a closing connection waits for the peer's close frame |
| `PingInterval` | off | Keepalive: ping this often and close when the peer stops answering |
| `PongTimeout` | 10s | How long a keepalive ping waits, meaningful only alongside `PingInterval` |
| `MessageLimits` | none | Quotas bounding how fast a peer may send |
| `Subprotocols` | none | The subprotocols the route can speak |
| `AllowedOrigins` | none | Browser origins allowed in addition to the server's own |
| `AllowOriginFunc` | none | A dynamic origin decision, consulted only for origins not already allowed |
| `InsecureSkipOriginCheck` | off | Accept a handshake from any origin |

Options can be set application-wide through `AppOptions.WebSocket` and narrowed for a
router or a route with `WithWebSocket`. Layering works field by field, so a route that
raises only the read limit keeps the application's origin policy.

`MaxConnections` and `MaxConnectionsPerIP` may only be set on the application: the resource
they protect is the process, not a route, so a router or a route that sets either is
refused when the application is built.

Keepalive only works while the handler is reading, because a pong is consumed by a read
like any other frame. A handler that only ever writes should ping by hand with `Ping`.

## The origin check

A cross-origin handshake is refused by default, and no CORS policy changes that.

A WebSocket handshake is not subject to the same-origin policy and is never preflighted,
which is what makes cross-site hijacking possible in the first place: without the check,
any page on the internet could open an authenticated connection to your server from a
visitor's browser, cookies and all. `AppOptions.CORS` has no bearing on it, for exactly that
reason.

```go
muzak.WSOptions{AllowedOrigins: []string{"https://app.example.com"}}
```

The server's own origin is always allowed. The single entry `"*"` allows any origin.

```go
muzak.WSOptions{
    AllowOriginFunc: func(r *http.Request, origin string) bool {
        return strings.HasSuffix(origin, ".example.com")
    },
}
```

It runs on every handshake an earlier rule did not already allow, so it must be cheap and
free of side effects.

`InsecureSkipOriginCheck` accepts a handshake from anywhere. It is safe only for a
connection that carries no ambient authority: one authenticated by a token the client has
to present explicitly, never by a cookie, since a browser attaches cookies to a
cross-origin handshake without being asked.

## Subprotocols

```go
muzak.WSOptions{Subprotocols: []string{"graphql-transport-ws", "graphql-ws"}}
```

The client's own list is in preference order, so the first of its choices that appears here
is the one negotiated. A client asking for something else is answered without the header,
which tells it to give up. Read the result with `conn.Subprotocol()`.

## Shutdown

Open connections are tracked, so a graceful shutdown tells every peer it is going away with
`1001` and waits for the handlers. `net/http` cannot do that on its own: a hijacked
connection is no longer one it knows about.

## Documentation

A WebSocket route appears in the OpenAPI document with its parameters and a
`101 Switching Protocols` response, because that is what OpenAPI can say about a handshake.
The conversation itself continues off the document.

## Testing

`muzak.WSDial` is the client half of the same engine, which is what lets a route be tested
over a real connection rather than against a second implementation. The test client wraps
it:

```go
func TestItemSocket(t *testing.T) {
    client := testclient.New(t, buildApp())

    conn := client.WS("/items/plumbus/ws", testclient.Query("token", "jessica"))

    if err := conn.WriteText(t.Context(), "hello"); err != nil {
        t.Fatalf("WriteText = %v", err)
    }
    reply, err := conn.ReadText(t.Context())
    if err != nil {
        t.Fatalf("ReadText = %v", err)
    }
    if !strings.Contains(reply, "jessica") {
        t.Errorf("reply = %q", reply)
    }
}

func TestItemSocketNeedsCredential(t *testing.T) {
    client := testclient.New(t, buildApp())

    _, response := client.TryWS("/items/plumbus/ws")

    response.AssertStatus(http.StatusUnauthorized)
}
```

Outside a test, dial directly:

```go
conn, _, err := muzak.WSDial(ctx, "ws://"+app.Addr()+"/items/plumbus/ws", muzak.WSDialOptions{})
if err != nil {
    return err
}
defer conn.Close(muzak.WSStatusNormalClosure, "")
```

The URL may use `ws`, `wss`, `http` or `https`. The response is returned alongside the
connection so a caller can read the handshake headers, and on failure the status and body
the server refused with. `WSDial` never follows a redirect, because following one would
send the handshake's headers to whatever host the answer named.

## Where to go next

[Server-Sent Events](/docs/realtime/server-sent-events) covers the simpler half
of what a WebSocket is usually reached for, and
[Rate Limiting](/docs/techniques/rate-limiting) covers the quotas
`MessageLimits` reuses.


--------------------------------------------------------------------------------
title: "Server-Sent Events"
description: "A typed event stream registered like any other route, read natively by a browser's EventSource, with the schema of one event in the generated document."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/realtime/server-sent-events"
--------------------------------------------------------------------------------

# Server-Sent Events

`Router.SSE` registers a route whose response is a stream rather than a body. It is the
other half of what a WebSocket is usually reached for, and the simpler half: the server
sends, the client listens, and a browser reads it natively with `EventSource`, reconnecting
on its own when a stream drops.

::code-group

```go [routers/items.go]
// An event stream route is declared like any other and answers 200 rather than
// upgrading: the guards run and the input binds before a byte of the stream is
// written, and the handler owns the stream until it returns.
r.SSE("/items/stream", handlers.StreamItems,
    muzak.Summary("Follow every change to the items"),
    muzak.WithSSE(muzak.SSEOptions{
        // A stream that says nothing for long enough is closed by proxies that
        // believe it to be idle, so a comment goes out instead.
        KeepAlive: 15 * time.Second,
        // A browser reconnects on its own when a stream ends, and this is how
        // soon.
        Retry: 2 * time.Second,
    }))
```

```go [handlers/items.go]
func StreamItems(ctx *muzak.Context, _ muzak.Empty, stream *muzak.SSEStream[schemas.ItemOut]) error {
    store := muzak.From[*core.ItemStore](ctx)

    updates := store.Watch(stream.Context())

    for {
        select {
        case <-stream.Context().Done():
            // The client went away or the server is shutting down. Either way
            // the conversation is over, and it is not a failure.
            return nil
        case change := <-updates:
            item := schemas.ItemOut{ID: change.Item.ID, Name: change.Item.Name}
            if err := stream.Send(item); err != nil {
                return err
            }
        }
    }
}
```

::

The type parameter is the contract: nothing but an `ItemOut` can be sent, and the generated
document describes the stream with that type, in the same way a handler's return type
describes an ordinary response.

```bash
curl -N 'http://localhost:8080/items/stream'
```

```
retry: 2000

event: item_update
id: 1
data: {"id":"plumbus","name":"Plumbus"}

event: item_update
id: 2
data: {"id":"fleeb","name":"Fleeb"}
```

The `retry` field is sent once, before anything else, because `SSEOptions.Retry` was set
on the route above: a client that loses the stream on the very first event already knows
how long to wait. The response carries `Content-Type: text/event-stream; charset=utf-8`,
`Cache-Control: no-cache, no-transform` and `X-Accel-Buffering: no`.

## An ordinary route

Middleware runs, guards run, dependencies resolve, and the input is bound and validated
**before a byte of the stream is written**. A request that fails any of that is answered
with the usual JSON error and never becomes a stream at all.

The response header is written before the handler is called, which is what lets a client
see the stream open immediately. Anything that decides whether to serve a stream at all
therefore belongs in a guard or a dependency, where there is still a response to say it in.

## The stream

Nothing on `SSEStream` takes a context, unlike `WSConn`, because a stream belongs to one
request.

| Call | What it does |
|---|---|
| `Send(data)` | One event carrying `data`, encoded as JSON into its data field |
| `SendEvent(event)` | One event described in full |
| `Comment(text)` | A comment line, which no client acts on and every client accepts |
| `Context()` | The context governing every send |
| `LastEventID()` | The identifier the client last saw |
| `Err()` | The error that ended the stream, or nil while it is usable |

`stream.Context()` is derived from the request's own and is cancelled when the client
disconnects, when the request ends, or when the server begins shutting down. That makes it
the one thing a handler has to watch, and every send after it reports
`muzak.ErrSSEStreamEnded`, so a handler that only sends ends on its own.

```go
for update := range updates {
    if err := stream.Send(update); err != nil {
        return err
    }
}
```

A handler that returns `ErrSSEStreamEnded` is treated as a stream that finished rather than
one that failed, so a client going away is not logged as an error. Test for it with
`errors.Is`; the reason the stream ended is wrapped inside.

Writes are serialized, so any number of goroutines may write to one stream and each event
goes out whole.

A stream is single use. The first failure ends it, and every later send reports that same
error rather than writing into a response nobody is reading. Sends that report a mistake
instead, such as an event name carrying a line break, leave the stream usable.

## Events with more than data

```go
type SSEEvent[Out any] struct {
    Name    string        // the type a browser dispatches it under
    ID      string        // what a reconnect resumes from
    Retry   time.Duration // how long to wait before reconnecting
    Comment string        // text no client acts on
    Data    *Out          // the value, encoded as JSON
    Text    string        // a payload written as it stands
}
```

```go [handlers/items.go]
// sendChange writes one change as an event a browser can listen for by name
// and resume from by identifier.
func sendChange(stream *muzak.SSEStream[schemas.ItemOut], change core.Change) error {
    item := schemas.ItemOut{ID: change.Item.ID, Name: change.Item.Name}
    return stream.SendEvent(muzak.SSEEvent[schemas.ItemOut]{
        Name: "item_update",
        ID:   strconv.Itoa(change.Seq),
        Data: &item,
    })
}
```

```js
const source = new EventSource("/items/stream")
source.addEventListener("item_update", (event) => {
    const item = JSON.parse(event.data)
    // ...
})
```

`Data` is a pointer so that an event carrying nothing can be told from one carrying a zero
value, and it cannot be combined with `Text`. An event setting both is refused.

`Name` and `ID` may not contain a line break. An event stream is a sequence of lines, so a
break in one of those fields would end it and let whatever followed be read as fields of
its own, which on a stream carrying one client's input to another is event forgery. Such a
send is refused rather than repaired, and the stream stays usable.

### Payloads that are not JSON

`Text` writes a payload as it stands, for a log line or for the sentinel some completion
APIs end with. A stream whose events are all text declares `muzak.Empty` as its model, and
the generated document then says there is no schema rather than describing one that does
not exist.

```go [handlers/chat.go]
// StreamChat answers a prompt one token at a time, which is the shape every
// chat completion API streams in.
func StreamChat(ctx *muzak.Context, in schemas.ChatIn, stream *muzak.SSEStream[muzak.Empty]) error {
    for word := range strings.SplitSeq(in.Text, " ") {
        select {
        case <-stream.Context().Done():
            // The client closed the tab, or the server is shutting down. There
            // is no one left to answer.
            return nil
        case <-time.After(tokenDelay):
        }
        if err := stream.SendEvent(muzak.SSEEvent[muzak.Empty]{Name: "token", Text: word}); err != nil {
            return err
        }
    }
    // The sentinel some clients expect at the end of a completion. It is text
    // rather than a value, which is what Text is for.
    return stream.SendEvent(muzak.SSEEvent[muzak.Empty]{Name: "done", Text: "[DONE]"})
}
```

A payload spanning several lines is written as several data lines and arrives whole, which
is both what the format asks for and what stops a value from ending its own field.

## A stream reached by POST

A stream is not tied to `GET`. `Router.SSEHandle` registers one for any method, which is
what a protocol that streams its answer to a posted document needs, and there the input
binds a request body like any other route.

::code-group

```go [routers/chat.go]
r.SSEHandle(http.MethodPost, "/chat/stream", handlers.StreamChat,
    muzak.Summary("Answer a prompt one token at a time"),
    muzak.WithSSE(muzak.SSEOptions{
        // A completion is never quiet for long, so the keepalive is only there
        // for the pause before the first token.
        KeepAlive: 10 * time.Second,
    }))
```

```go [schemas/chat.go]
// ChatIn is the prompt a chat stream answers.
//
// It is an ordinary JSON body: an event stream reached by POST binds and
// validates one exactly as any other route does.
type ChatIn struct {
    Text string `json:"text" doc:"The prompt to answer, one token at a time"`
}

// Validate bounds the prompt, and is applied before a byte of the stream is
// written.
func (in *ChatIn) Validate(v *muzak.Validation) {
    v.String(&in.Text).Trim().Required().MinLen(1).MaxLen(280)
}
```

::

```bash
curl -N -X POST 'http://localhost:8080/chat/stream' \
     -H 'Content-Type: application/json' -d '{"text":"what is a plumbus"}'
```

## Resuming a dropped stream

A browser remembers the last identifier it saw and sends it back in `Last-Event-ID` when it
reconnects. `stream.LastEventID()` reads it, and that is what turns a dropped connection
into a stream that picks up where it left off rather than one that starts again.

```go [handlers/items.go]
func StreamItems(ctx *muzak.Context, _ muzak.Empty, stream *muzak.SSEStream[schemas.ItemOut]) error {
    store := muzak.From[*core.ItemStore](ctx)

    // A browser sends back the identifier of the last event it saw when its
    // EventSource reconnects. The value is the client's, so one that is not a
    // number is treated as no value at all rather than as an error.
    seen := 0
    if last := stream.LastEventID(); last != "" {
        if parsed, err := strconv.Atoi(last); err == nil {
            seen = parsed
        }
    }

    // Subscribing before the backlog is read is what stops a change made in
    // between from falling through the gap between the two.
    updates := store.Watch(stream.Context())
    for _, change := range store.Since(seen) {
        if err := sendChange(stream, change); err != nil {
            return err
        }
        seen = change.Seq
    }

    for {
        select {
        case <-stream.Context().Done():
            return nil
        case change := <-updates:
            if change.Seq <= seen {
                // Already sent from the backlog above.
                continue
            }
            if err := sendChange(stream, change); err != nil {
                return err
            }
            seen = change.Seq
        }
    }
}
```

`LastEventID` is a value the client controls, so treat it as input rather than as a cursor
to be trusted.

## What a stream bounds

A stream costs a connection and a goroutine for as long as a client cares to hold it.

| A client that... | ...is stopped by |
|---|---|
| stops reading what it asked for | writes give up after `WriteTimeout` rather than pinning a goroutine and a growing socket buffer |
| opens streams without end | `MaxStreams` per application, then `503` with `Retry-After` |
| opens many from one address | `MaxStreamsPerIP`, so one client cannot take every slot the process has |
| holds a stream open for hours | the listener's own timeouts are cleared for it and replaced with a deadline per event, so a healthy stream is never cut off and an unhealthy one still is |
| sends a `Last-Event-ID` a handler echoes | a name or identifier with a line break in it is refused |
| reads through a buffering proxy | `Cache-Control: no-cache, no-transform`, `X-Accel-Buffering: no`, and a keepalive comment every `KeepAlive` |

Clearing the listener's read and write deadlines matters more than it sounds. A stream is a
response that does not end, so it would otherwise die at `ServerOptions.WriteTimeout`
however healthy it was, and the read deadline would cancel the request, and with it the
stream, at `ServerOptions.ReadTimeout` and blame the client.

## Options

| Field | Default | Effect |
|---|---|---|
| `KeepAlive` | 15s | How often a comment is written to a stream that has sent nothing. A negative value turns it off |
| `WriteTimeout` | 10s | How long one event may take to reach the client |
| `Retry` | unset | The reconnection delay advertised at the start of every stream |
| `MaxStreams` | 1024 | Streams the application serves at once. Application-level only |
| `MaxStreamsPerIP` | 64 | Streams one address holds at once. Application-level only |

Set them application-wide through `AppOptions.SSE` and narrow them for a router or a route
with `WithSSE`. Layering works field by field, so a route that only lengthens the keepalive
keeps the application's write timeout.

`MaxStreams` and `MaxStreamsPerIP` may only be set on the application: the resource they
protect is the process, not a route, so a router or a route that sets either is refused when
the application is built.

The keepalive exists because a proxy that sees an idle connection for long enough closes
it, and because a silent stream is indistinguishable from a dead one.

## What does not apply

**Compression leaves an event stream alone.** Holding events in a compressor's window until
something forces them out is the one thing a stream cannot survive.

**The origin check a WebSocket needs has no counterpart here.** An `EventSource` is subject
to the same-origin policy and to CORS like any other request, so `AppOptions.CORS` already
governs it. See [CORS](/docs/security/cors).

**Nothing a handler fails with is disclosed.** The response header went out before the
handler ran, so a failure ends the stream and the reason goes to the log.

## Shutdown

Open streams are tracked, so a graceful shutdown ends every one of them and waits for the
handlers, instead of waiting out its whole deadline once per stream.

## Documentation

An SSE route is documented with a `200` response carrying `text/event-stream`, whose schema
describes the data field of a single event. OpenAPI 3.1 has no way to say "many of these,
one after another", and one event's data is the only part a client has to be able to
decode. A stream whose model is `muzak.Empty` is documented with no schema at all.

## Testing

```go
func TestStreamItems(t *testing.T) {
    client := testclient.New(t, buildApp())

    stream := client.SSE("/items/stream")

    client.Post("/items/", testclient.JSON(map[string]string{"id": "plumbus", "name": "Plumbus"})).
        AssertStatus(http.StatusCreated)

    item := stream.Decode[schemas.ItemOut]()
    if item.ID != "plumbus" {
        t.Errorf("id = %q, want plumbus", item.ID)
    }
}
```

`muzak.SSEDial` is the reading half of the same engine, so a route is tested over a real
connection rather than against a second implementation:

```go
reader, _, err := muzak.SSEDial(ctx, "http://"+app.Addr()+"/items/stream", muzak.SSEDialOptions{})
if err != nil {
    return err
}
defer reader.Close()

for {
    message, err := reader.Next(ctx)
    if errors.Is(err, muzak.ErrSSEStreamEnded) {
        return nil
    }
    if err != nil {
        return err
    }
    item, err := message.Decode[schemas.ItemOut]()
    if err != nil {
        return err
    }
    _ = item
}
```

A response that is not `200` with a `text/event-stream` body is refused rather than parsed,
because a stream reader that quietly accepts an HTML error page reports "no events" for what
is actually a failure. `SSEDialOptions` carries the method, body, headers, a `LastEventID`
to resume from, a read limit, a read timeout and `KeepComments` for seeing the keepalives.

## Where to go next

[WebSockets](/docs/realtime/websockets) covers the two-way half, and
[Testing](/docs/fundamentals/testing) covers asserting on a stream.


--------------------------------------------------------------------------------
title: "Safe Defaults"
description: "Every default in Muzak is the conservative one. This is the full list, what each one stops, and how to relax it deliberately."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/security/safe-defaults"
--------------------------------------------------------------------------------

# Safe Defaults

Muzak starts from settings that are safe rather than permissive. Each of them can be
relaxed; none of them is relaxed by omission.

## The list

| Area | Default | Relax it with |
|---|---|---|
| Listener timeouts | All four are non-zero. An `http.Server` left at its zero values holds a connection open forever, which is all a slow-loris client needs | `ServerOptions`, a negative value to disable one |
| Request bodies | Capped at 1 MiB | `AppOptions.MaxBodySize`, `MaxBodySize(n)` |
| Form bodies | Capped at 32 MiB, refused with `413` while being read | `AppOptions.MaxUploadSize`, `MaxUploadSize(n)` |
| Request headers | The header block is capped at 1 MiB | `ServerOptions.MaxHeaderBytes` |
| Unknown JSON members | Rejected. A client's typo becomes an immediate `422` instead of a silently dropped value | `AllowUnknownFields()` |
| Duplicate members, invalid UTF-8 | Rejected by `encoding/json/v2` | not relaxable |
| Cross-origin requests | Denied. No CORS header is emitted until a policy is configured, and a wildcard origin with credentials is refused outright | `AppOptions.CORS` |
| Cross-origin WebSocket handshakes | Refused. A handshake is not subject to the same-origin policy and is never preflighted, so CORS cannot cover it | `WSOptions.AllowedOrigins` |
| WebSocket messages | Capped at 1 MiB, refused before any of the payload is buffered, read a chunk at a time | `WSOptions.ReadLimit` |
| WebSocket message time | One message has 30 seconds to arrive once it has begun, and a bounded number of frames to arrive in. Idle connections are left alone | `WSOptions.ReadTimeout` |
| WebSocket connections | 1024 per application and 64 per address, then `503` with a `Retry-After` | `WSOptions.MaxConnections`, `MaxConnectionsPerIP` |
| WebSocket extensions | None negotiated, so no peer can ask the server to hold compression state on its behalf | not relaxable |
| Event stream writes | Given up on after 10 seconds, so a client that opens a stream and never reads it cannot pin a goroutine and a growing socket buffer | `SSEOptions.WriteTimeout` |
| Event streams | 1024 per application and 64 per address, with a keepalive comment every 15 seconds | `SSEOptions.MaxStreams`, `MaxStreamsPerIP`, `KeepAlive` |
| Event fields | A name or identifier carrying a line break is refused, because it would end its own field and let what follows be read as events of its own | not relaxable |
| Panics | Logged with a full stack trace, answered with a generic `500`. Nothing derived from the panic reaches the client | not relaxable |
| Errors | An error that does not describe itself becomes an opaque `500` with the real cause logged and never transmitted | `AppOptions.ErrorRenderer` |
| Request identifiers | Not trusted from the client, because an attacker-controlled identifier is an attacker-controlled log field | `AppOptions.TrustRequestIDHeader` |
| Forwarding headers | Not believed. `X-Forwarded-For` is read only for a request that arrived from a proxy named in `TrustedProxies` | `AppOptions.ClientIP` |
| Rate limiting | Off until a policy names a quota. Once one exists, counters are bounded in number and in key length, and a storage that stops answering refuses traffic | `AppOptions.RateLimit`, `FailOpen` |
| Secrets in logs | Attribute keys such as `authorization`, `token` and `api_key` are redacted | `LoggerOptions.RedactKeys` |
| Token comparison | Constant-time, over hashed inputs, so neither the contents nor the length of a secret leaks through timing | `muzak.SecureCompare` |
| Security headers | `X-Content-Type-Options`, `X-Frame-Options` and a referrer policy on every response | `AppOptions.DisableSecurityHeaders` |
| Documentation UI | Not served at all until `AppOptions.DocsUI` names one. A page that is configured is embedded in the binary, fetches nothing from a third party, and is served under a content security policy that hashes its own inline script and permits no network access beyond this origin | `AppOptions.DocsUI`, `AppOptions.DisableDocs` |
| Validation | Automatic for any model that declares rules. There is no option to remember, so a model cannot be left unvalidated by forgetting one | `SkipValidation()` |
| Versioning | Off until a type is named. Once on, a route that declares no version answers nothing rather than being served unversioned, so a resource is opted into being version-independent deliberately | `AppOptions.Versioning`, `WithVersion`, `VersionNeutral` |

## The one deliberate exception

Rate limiting is off until a quota is declared. There is no limit that is right for every
application, and a default one would be a number nobody chose refusing traffic nobody
expected.

What is safe by default is what happens once a policy exists: the counters are bounded, the
address is not taken from a header anyone can write, and a storage that stops answering
stops traffic rather than stopping the limit. See
[Rate Limiting](/docs/techniques/rate-limiting).

## The defaults as constants

Every one of them is exported, so a deployment can read them rather than guess.

```go
muzak.DefaultMaxBodySize          // 1 << 20
muzak.DefaultMaxUploadSize        // 32 << 20
muzak.DefaultMaxHeaderBytes       // 1 << 20

muzak.DefaultReadHeaderTimeout    // 5s
muzak.DefaultReadTimeout          // 30s
muzak.DefaultWriteTimeout         // 30s
muzak.DefaultIdleTimeout          // 120s
muzak.DefaultShutdownTimeout      // 15s

muzak.DefaultWSReadLimit          // 1 << 20
muzak.DefaultWSReadTimeout        // 30s
muzak.DefaultWSWriteTimeout       // 10s
muzak.DefaultWSMaxConnections     // 1024
muzak.DefaultWSMaxConnectionsPerIP // 64
muzak.DefaultWSCloseGracePeriod   // 250ms
muzak.DefaultWSPongTimeout        // 10s

muzak.DefaultSSEKeepAlive         // 15s
muzak.DefaultSSEWriteTimeout      // 10s
muzak.DefaultSSEMaxStreams        // 1024
muzak.DefaultSSEMaxStreamsPerIP   // 64

muzak.DefaultRateLimitMaxEntries  // 100000
muzak.DefaultRateLimitSweepInterval // 1m

muzak.DefaultCompressionMinSize   // 1400
muzak.DefaultForwardedHeader      // "X-Forwarded-For"
```

## Things the type system does rather than a default

Some of what would be a runtime setting elsewhere is not a setting here at all.

**A response cannot leak a field it does not declare.** The handler's return type is the
response model. There is no filtering pass to configure and no `response_model` to forget,
because a field that is not on `Out` has no way to be written.

```go
// Adding a column to this changes nothing a client can see.
type Item struct {
    ID           string
    Name         string
    InternalNote string
}

// Only these three members exist on the wire.
type ItemOut struct {
    ID   string `json:"id"`
    Name string `json:"name"`
}
```

**A crafted body cannot reach a located field.** When an input mixes a path parameter with
body members, the body is decoded into a scratch value and only the body-bound fields are
copied out.

**Two concurrent requests cannot see each other's dependencies.** Resolved values live on
the request's `Context` and are cleared when it returns to the pool. There is a test for
exactly that, run under the race detector.

**A misconfiguration is a build error, not a surprise.** Duplicate routes, unbindable input
types, path parameters no field binds, duplicate operation identifiers, malformed prefixes,
a wildcard CORS origin combined with credentials, a `MaxConnections` set on a route, a
missing static directory: all reported together when the application is built, before a
socket is opened.

## A deployment checklist

Things worth deciding explicitly, because the safe default may not be the correct one for
your deployment:

- **`ClientIP.TrustedProxies`** if anything sits in front of the service, or every request
  is attributed to the proxy. See [Behind a Proxy](/docs/deployment/behind-a-proxy).
- **`CORS`** if a browser on another origin calls the API. See [CORS](/docs/security/cors).
- **`WSOptions.AllowedOrigins`** if a browser opens WebSocket connections.
- **A rate limit policy**, and a shared storage if the service runs more than once.
- **`Secure: true`** on every cookie once the service is served over TLS. See
  [Cookies](/docs/techniques/cookies).
- **`DisableDocs`** if the deployment must not describe itself.
- **A `Servers` list** in the OpenAPI options, so a generated client points at the right
  host.

## Where to go next

[Authentication](/docs/security/authentication) covers proving who a caller is,
and [Behind a Proxy](/docs/deployment/behind-a-proxy) covers the one default that
is wrong in the other direction behind a load balancer.


--------------------------------------------------------------------------------
title: "Authentication"
description: "Shared-secret guards, bearer tokens, sessions, per-user identity and webhook signatures over the raw body, resolved before the handler runs and compared in constant time."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/security/authentication"
--------------------------------------------------------------------------------

# Authentication

Authentication in Muzak is a dependency. A **guard** rejects a request that cannot prove
who it is; a **provider** resolves the caller into a typed value the handler reads with
`muzak.From`. Both run before binding and before the handler, so an unauthenticated caller
gets `401` rather than a map of your schema.

## Shared secrets

For an internal service, a webhook receiver or an admin subtree, the credential is one
secret both sides know. Two guards ship with the framework.

```go
// An Authorization: Bearer <token> header.
muzak.RequireBearerToken(settings.AdminToken)

// Any named header, such as an internal service token or a webhook
// signing key.
muzak.RequireHeaderToken("X-Token", settings.AdminToken)
```

::code-group

```go [cmd/main.go]
app.Include(routers.Admin(),
    muzak.WithPrefix("/admin"),
    muzak.WithTags("admin"),
    muzak.WithDependencies(core.GetTokenHeader(settings)),
)
```

```go [core/dependencies.go]
// GetTokenHeader returns the guard applied where the admin router is included.
//
// The comparison is constant-time, so the response timing does not reveal how
// much of the supplied header was correct.
func GetTokenHeader(settings Settings) muzak.Guard {
    return muzak.RequireHeaderToken("X-Token", settings.AdminToken)
}
```

::

`RequireBearerToken` answers a missing credential with a `WWW-Authenticate` header so a
client knows which scheme to use. Both compare in constant time.

Note where the guard is attached. The admin router itself declares no prefix and no
authentication; both are applied where it is included, which keeps the router reusable and
puts the security decision somewhere a reviewer will find it.

## Comparing secrets

```go
if !muzak.SecureCompare(given, expected) {
    return muzak.Unauthorized("unauthorized")
}
```

Comparing a credential with `==` leaks its contents: the comparison returns as soon as two
bytes differ, so an attacker who can time the response can recover the secret one byte at a
time. `SecureCompare` hashes both inputs first and compares the digests, which also keeps
the length of the expected secret from leaking.

Use it for tokens, API keys and signatures. It is **not** a password verification function.
A password must be checked against a slow, memory-hard hash such as the one
`golang.org/x/crypto/argon2` provides.

## Resolving a user

A per-user credential wants a provider, so the identity reaches the handler as a value.

::code-group

```go [core/dependencies.go]
// CurrentUser is the authenticated caller.
//
// It is produced once per request by GetCurrentUser and read inside a handler
// with muzak.From[core.CurrentUser](ctx), where the type is checked by the
// compiler and no cast is written anywhere.
type CurrentUser struct {
    Username string
}

// GetCurrentUser is a value dependency that resolves the caller from the
// Authorization header.
func GetCurrentUser(ctx *muzak.Context) (CurrentUser, error) {
    token, present := muzak.BearerToken(ctx)
    if !present {
        return CurrentUser{}, muzak.Unauthorized("unauthorized")
    }

    users := muzak.From[*core.UserStore](ctx)
    user, err := users.ByToken(ctx.Context(), token)
    if err != nil {
        // The same answer whether the token was unknown or the lookup failed,
        // so a caller cannot tell one from the other.
        return CurrentUser{}, muzak.Unauthorized("unauthorized")
    }
    return CurrentUser{Username: user.Username}, nil
}
```

```go [routers/items.go]
r.Get("/items/{item_id}", handlers.ReadItem,
    muzak.Summary("Read an item"),
    // Only this route resolves the caller, so only this route pays for it.
    muzak.Needs(core.GetCurrentUser))
```

```go [handlers/items.go]
func ReadItem(ctx *muzak.Context, in schemas.ItemParams) (schemas.ItemOut, error) {
    // Both type arguments are checked at compile time and no cast appears here.
    store := muzak.From[*core.ItemStore](ctx)
    user := muzak.From[core.CurrentUser](ctx)

    item, err := store.Get(in.ID)
    if err != nil {
        return schemas.ItemOut{}, asHTTPError(err)
    }
    return schemas.ItemOut{ID: item.ID, Name: item.Name, Owner: user.Username}, nil
}
```

::

`muzak.Needs` on a router applies to every route beneath it, which is how a whole
authenticated subtree is declared once.

`muzak.BearerToken(ctx)` reads the `Authorization` header and reports whether a
well-formed bearer credential was present. The scheme is matched case-insensitively, as RFC
9110 requires.

## Sessions

A browser flow exchanges credentials for a session cookie once, then presents the cookie.

::code-group

```go [handlers/login.go]
func Login(ctx *muzak.Context, in schemas.LoginIn) (schemas.LoginOut, error) {
    expected, known := accounts[in.Username]

    // The comparison runs even for an unknown account, and the same answer is
    // given either way. Returning "no such user" would turn this endpoint into
    // a way to enumerate accounts, and returning early would let its timing do
    // the same thing more quietly.
    matches := subtle.ConstantTimeCompare([]byte(expected), []byte(in.Password)) == 1
    if !known || !matches {
        return schemas.LoginOut{}, muzak.Unauthorized("the username or password is incorrect")
    }

    session := uuid.NewV4().String()
    ctx.SetCookie(&http.Cookie{
        Name:     "session_id",
        Value:    session,
        Path:     "/",
        HttpOnly: true,
        SameSite: http.SameSiteLaxMode,
        Secure:   true,
        MaxAge:   3600,
    })

    return schemas.LoginOut{Username: in.Username, SessionID: session}, nil
}
```

```go [routers/login.go]
r.Post("/login/", handlers.Login,
    muzak.Summary("Exchange a username and password for a session"),
    muzak.WithResponseDoc(http.StatusUnauthorized, "The username or password is incorrect"),
    // Stricter than the rest of the application, because guessing a password is
    // the one request worth making a hundred times a minute. The count runs
    // before the handler, so a wrong password costs the same budget as a right
    // one.
    muzak.RateLimit(muzak.Quota{Name: "login", Window: time.Minute, Limit: 5}))
```

::

Four things that matter on a sign-in route, all visible above:

- **The same answer either way.** Telling an unknown account from a wrong password turns
  the endpoint into an account enumerator.
- **The same timing either way.** Returning early on an unknown account says the same thing
  more quietly, which is why the comparison runs regardless.
- **A rate limit counted before the guards.** A request a guard rejects is still counted, so
  failed sign-ins cost the attacker their budget. `AfterDependencies` would give that up,
  which is why a login route never uses it.
- **A password checked against a slow hash.** The example compares plain values because
  there is nothing to protect in it. A real one stores an argon2 or bcrypt digest.

Resolve the session on the way back in with a provider:

```go
func GetSession(ctx *muzak.Context) (Session, error) {
    cookie, err := ctx.Cookie("session_id")
    if err != nil || cookie.Value == "" {
        return Session{}, muzak.Unauthorized("sign in first")
    }
    sessions := muzak.From[*core.SessionStore](ctx)
    return sessions.Lookup(ctx.Context(), cookie.Value)
}
```

## Credentials on a WebSocket

A browser cannot set headers on a WebSocket handshake, so the two places a credential can
arrive are a cookie the browser attaches itself and a query parameter the page puts in the
URL.

```go [core/dependencies.go]
// SessionOrToken is the caller of a WebSocket route, resolved from either a
// session cookie or a query parameter.
type SessionOrToken struct {
    Value      string
    FromCookie bool
}

// GetSessionOrToken resolves the caller of a WebSocket route.
//
// It runs during the handshake, before a single byte is upgraded, so a caller
// with no credential receives an ordinary JSON error rather than a connection
// that closes a moment later.
func GetSessionOrToken(ctx *muzak.Context) (SessionOrToken, error) {
    if cookie, err := ctx.Cookie("session"); err == nil && cookie.Value != "" {
        return SessionOrToken{Value: cookie.Value, FromCookie: true}, nil
    }
    if token := ctx.Query("token"); token != "" {
        return SessionOrToken{Value: token}, nil
    }
    return SessionOrToken{}, muzak.Unauthorized("a session cookie or a token query parameter is required")
}
```

A cookie is ambient authority a browser attaches without being asked, which is exactly why
a cross-origin handshake is refused by default and why `InsecureSkipOriginCheck` is only
safe for a connection authenticated by an explicit token. See
[WebSockets](/docs/realtime/websockets).

A token in a query string ends up in access logs and browser history. Muzak's own access
log records no query strings for that reason; whatever is in front of the service may not
be as careful.

## Verifying a signed token

A JWT or a signed session is verified in a provider like anything else. Nothing about it is
special to the framework.

```go
func GetClaims(ctx *muzak.Context) (Claims, error) {
    token, present := muzak.BearerToken(ctx)
    if !present {
        return Claims{}, muzak.Unauthorized("unauthorized")
    }

    keys := muzak.From[*core.KeySet](ctx)
    claims, err := keys.Verify(token)
    if err != nil {
        // The reason stays server-side. Telling a caller which check failed
        // tells an attacker which one to work on next.
        return Claims{}, muzak.Unauthorized("unauthorized").Wrap(err)
    }
    if claims.ExpiresAt.Before(time.Now()) {
        return Claims{}, muzak.Unauthorized("the token has expired")
    }
    return claims, nil
}
```

`Wrap` records the real cause in the log and keeps it out of the response. See
[Error Handling](/docs/getting-started/error-handling).

The key set is a resource, so build it once and publish it with `WithSingleton`. If it
refreshes from a remote endpoint, implement `muzak.Lifecycle` and let the application start
and stop it. See [Lifecycle](/docs/getting-started/lifecycle).

## Verifying a signature over the body

A signed *token* is verified from a header. A signed *request* is verified from its bytes,
and those are a different problem: the binder reads the body to decode the input, so by the
time a handler runs there is nothing left to hash.

Declare `muzak.CaptureBody()` and the bytes are kept for the request.

```go
r.Post("/webhooks/stripe", handlers.Stripe,
    muzak.CaptureBody(),
    muzak.Needs(core.VerifyStripeSignature))
```

```go
func VerifyStripeSignature(ctx *muzak.Context) (Verified, error) {
    body, ok := ctx.RawBody()
    if !ok {
        // The route did not declare CaptureBody. Refusing is the only safe
        // answer: verifying against an empty body would pass for a request
        // that carried no signature at all.
        return Verified{}, muzak.NewHTTPError(
            http.StatusInternalServerError, "this route does not capture its body")
    }

    expected := hmac.New(sha256.New, secret)
    expected.Write(body)
    if !hmac.Equal(expected.Sum(nil), presented) {
        // The same answer whichever part was wrong, as everywhere else on this
        // page.
        return Verified{}, muzak.Unauthorized("the signature does not match")
    }
    return Verified{}, nil
}
```

**Verify the original, never a re-encoding of it.** Marshalling the bound input back to
JSON produces a different document — members reordered, numbers normalised, whitespace
gone — and a signature over it never matches. The failure looks like a bug in the client's
signing code, which is where people go looking, and it is not there.

Capture runs before the dependencies, so this guard refuses an unsigned request before
anything decodes it, and after the rate limit is counted, so a client past its budget costs
the server no buffering. See
[Request Data](/docs/getting-started/request-data) for the size limit and the
cost.

## Documenting an authenticated route

The credential itself is not part of the generated document, so document the outcome:

```go
r.Get("/users/me", handlers.CurrentUser,
    muzak.Summary("Read the authenticated user"),
    muzak.Needs(core.GetCurrentUser),
    muzak.WithResponseDoc(http.StatusUnauthorized, "No usable credential was presented"))
```

Applied to a router or at an include, it documents the response for every route beneath.

## Testing

```go
func TestReadItemBadToken(t *testing.T) {
    client := testclient.New(t, buildApp())

    res := client.Get("/items/foo", testclient.Header("X-Token", "hailhydra"))

    res.AssertStatus(http.StatusUnauthorized)
    res.AssertErrorCode(muzak.CodeUnauthorized)
}
```

```go
// authorized is the header every request in this suite needs.
func authorized() testclient.Option {
    return testclient.WithHeader("X-Token", "coneofsilence")
}

client := testclient.New(t, buildApp(), authorized())
```

## Where to go next

[Authorization](/docs/security/authorization) covers what a resolved caller is
allowed to do, and [Cookies](/docs/techniques/cookies) covers the attributes that
decide whether a session is safe.


--------------------------------------------------------------------------------
title: "Authorization"
description: "Deciding what an authenticated caller may do, with guards that cover a subtree, checks that need the resolved identity, and ownership checks in the handler."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/security/authorization"
--------------------------------------------------------------------------------

# Authorization

Authentication answers "who is this". Authorization answers "may they do this". In Muzak
the second is a guard, a provider, or a check in the handler, depending on what the answer
depends on.

| The decision depends on | Put it in |
|---|---|
| The route alone | A guard on the router, declared where it is included |
| The caller's identity or role | A guard or provider that reads the resolved caller |
| The specific record being touched | The handler, after the record is loaded |

## Authorizing a whole subtree

The clearest place for a subtree decision is where the subtree is mounted.

```go [cmd/main.go]
// The admin router is written without a prefix or a guard. Both are applied
// here, which is what keeps that router reusable and puts the security decision
// somewhere a reviewer will find it.
app.Include(routers.Admin(),
    muzak.WithPrefix("/admin"),
    muzak.WithTags("admin"),
    muzak.WithDependencies(core.GetTokenHeader(settings)),
    muzak.WithResponseDoc(http.StatusForbidden, "The caller is not an administrator"),
)
```

Guards run in declaration order, outermost first: those declared on the application run
before those declared at the include, which run before the route's own. The first guard to
return an error stops the chain and produces the response.

```go [cmd/main.go]
app := muzak.New(muzak.AppOptions{Title: settings.AppName},
    // Runs for every route in the application, including the admin subtree.
    muzak.WithDependencies(core.GetQueryToken),
)
```

## Authorizing on a role

Every guard on a request runs before any provider does, whatever scope each was declared
on. A guard therefore cannot read a value a provider produced: `muzak.From` would panic
and `muzak.TryFrom` would report that nothing was resolved.

That is not a limitation to work around. A role check is part of resolving the caller, so
it belongs in the provider, and the result is a type that only exists when the check
passed.

::code-group

```go [core/authorization.go]
// Admin is a caller who has been shown to hold the admin role.
//
// Having one is proof of the check, because the only thing that produces one is
// the provider below.
type Admin struct {
    Username string
}

// GetAdmin resolves the caller and insists they are an administrator.
func GetAdmin(ctx *muzak.Context) (Admin, error) {
    user, err := GetCurrentUser(ctx)
    if err != nil {
        return Admin{}, err
    }
    if !slices.Contains(user.Roles, "admin") {
        return Admin{}, muzak.Forbidden("this action requires the admin role")
    }
    return Admin{Username: user.Username}, nil
}
```

```go [routers/admin.go]
func Admin() *muzak.Router {
    r := muzak.NewRouter(muzak.Needs(core.GetAdmin))

    r.Post("/", handlers.AdminAction,
        muzak.Status(http.StatusCreated),
        muzak.Summary("Admin action"),
        muzak.Description("Performs a privileged action. Requires the admin role."))

    return r
}
```

```go [handlers/admin.go]
func AdminAction(ctx *muzak.Context, in schemas.AdminActionIn) (schemas.AdminActionOut, error) {
    // Reaching this line is proof of the check.
    admin := muzak.From[core.Admin](ctx)

    return schemas.AdminActionOut{Name: in.Name, Message: "acted on by " + admin.Username}, nil
}
```

::

`Needs` on a router applies to every route beneath it, so the whole subtree is covered by
one line.

When a check does not need the caller's identity, a guard is the simpler tool, and it can
read anything on the request directly:

```go
// RequireMaintenanceWindow refuses a destructive route outside the window it is
// allowed to run in. It reads nothing a provider produced, so a guard is enough.
func RequireMaintenanceWindow(ctx *muzak.Context) error {
    if !inMaintenanceWindow(time.Now()) {
        return muzak.Forbidden("this action is only available during the maintenance window")
    }
    return nil
}
```

## 401 against 403

| Status | Means |
|---|---|
| `401 Unauthorized` | No usable credential was presented, or the one presented could not be verified |
| `403 Forbidden` | The credential was understood, and it is not enough |

The distinction matters to a client: a `401` says "sign in", a `403` says "signing in again
will not help". Muzak classifies them as `unauthorized` and `forbidden`, which is the
`code` a client should branch on rather than the status.

Where the status alone is too coarse, add a classifier of your own:

```go
return muzak.Forbidden("this workspace is on the free plan").
    WithCode("plan_upgrade_required")
```

## Authorizing a specific record

Ownership is not knowable until the record is loaded, so it belongs in the handler.

```go [handlers/items.go]
func UpdateItem(ctx *muzak.Context, in schemas.ItemUpdateIn) (schemas.ItemOut, error) {
    store := muzak.From[*core.ItemStore](ctx)
    user := muzak.From[core.CurrentUser](ctx)

    item, err := store.Get(in.ID)
    if err != nil {
        return schemas.ItemOut{}, asHTTPError(err)
    }
    if item.Owner != user.Username {
        // The same answer a missing item gets. Telling a caller that an item
        // exists but is not theirs turns the endpoint into a way to enumerate
        // other people's identifiers.
        return schemas.ItemOut{}, muzak.NotFound("Item not found")
    }

    updated, err := store.Rename(in.ID, in.Name)
    if err != nil {
        return schemas.ItemOut{}, asHTTPError(err)
    }
    return schemas.ItemOut{ID: updated.ID, Name: updated.Name, Owner: user.Username}, nil
}
```

Answering `404` rather than `403` for someone else's record is a deliberate choice: a `403`
confirms the record exists. Use it where the existence of the record is not itself a
secret, and `404` where it is.

## Authorization and the response body

The strongest guarantee here is not a check at all. The handler's return type is the
response model, so a field a caller must not see cannot be returned by accident: it is not
part of the type.

```go
// The store's own type. Adding a field here changes nothing a client can see.
type Item struct {
    ID           string
    Name         string
    Owner        string
    InternalNote string
}

// What a client receives. Owner is present only where the route resolves the
// caller, and InternalNote has no way to be written at all.
type ItemOut struct {
    ID    string `json:"id"`
    Name  string `json:"name"`
    Owner string `json:"owner,omitzero"`
}
```

Where two audiences need two shapes, write two output types and two routes rather than one
type with a filtering step.

## Ordering, once more

For every request:

1. Middleware.
2. The rate limit count, unless the policy defers it.
3. Guards, outermost first.
4. Providers, in declaration order.
5. Binding.
6. Validation.
7. The handler.

Guards running before validation is what keeps an unauthenticated caller from learning your
schema by sending it garbage.

Rate limiting running before the guards is what makes a rejected request still cost the
caller something. See [Rate Limiting](/docs/techniques/rate-limiting).

## Hiding an endpoint is not authorizing it

```go
r.Get("/healthz", handlers.Health, muzak.Hidden(), muzak.SkipRateLimit())
```

`Hidden` leaves a route fully routable and merely absent from the document. It is right for
a health check or an internal endpoint whose existence is uninteresting; it is not a
substitute for a guard.

## Testing an authorization rule

```go
func TestAdminActionRequiresTheRole(t *testing.T) {
    client := testclient.New(t, buildApp(), authorizedAs("editor"))

    res := client.Post("/admin/", testclient.JSON(map[string]string{"name": "plumbus"}))

    res.AssertStatus(http.StatusForbidden)
    res.AssertErrorCode(muzak.CodeForbidden)
}

func TestUpdatingSomebodyElsesItem(t *testing.T) {
    client := testclient.New(t, buildApp(), authorizedAs("morty"))

    res := client.Put("/items/ricks-portal-gun", testclient.JSON(map[string]string{"name": "mine now"}))

    res.AssertStatus(http.StatusNotFound)
}
```

A test per rule is cheap here, because the client exercises the whole chain: middleware,
guards, providers, binding and the handler.

## Where to go next

[Authentication](/docs/security/authentication) covers producing the identity
these rules read, and [Safe Defaults](/docs/security/safe-defaults) covers what
the framework refuses without being asked.


--------------------------------------------------------------------------------
title: "CORS"
description: "A cross-origin policy that denies everything until it is written down, refuses the one combination browsers reject anyway, and does not cover a WebSocket handshake."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/security/cors"
--------------------------------------------------------------------------------

# CORS

Cross-origin resource sharing is configured rather than installed. The zero value denies
every cross-origin request, and no CORS middleware is installed at all until a policy names
an origin or supplies a decision function.

That is the only safe default. A permissive policy set by accident hands any web page on
the internet the ability to read authenticated responses from the browser of anyone
visiting it.

## Configuring it

```go
app := muzak.New(muzak.AppOptions{
    Title: "Awesome API",
    CORS: muzak.CORSOptions{
        AllowedOrigins:   []string{"https://app.example.com", "https://admin.example.com"},
        AllowedMethods:   []string{"GET", "POST", "PATCH", "DELETE"},
        AllowedHeaders:   []string{"Content-Type", "Authorization", "X-Request-Id"},
        ExposedHeaders:   []string{"X-Request-Id", "RateLimit-Remaining"},
        AllowCredentials: true,
        MaxAge:           10 * time.Minute,
    },
})
```

| Field | Default | Effect |
|---|---|---|
| `AllowedOrigins` | none | The exact origins permitted. The single entry `"*"` allows any |
| `AllowOriginFunc` | none | A dynamic decision, consulted only for an origin `AllowedOrigins` did not already allow |
| `AllowedMethods` | `GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS` | The methods a cross-origin request may use |
| `AllowedHeaders` | `Content-Type, Authorization, X-Request-Id` | The request headers a client may send |
| `ExposedHeaders` | none | The response headers a client may read. Browsers expose only a small safelist otherwise |
| `AllowCredentials` | off | Permits cookies and `Authorization` headers on cross-origin requests |
| `MaxAge` | 10 minutes | How long a browser may cache the preflight result. Browsers cap it regardless |

Origins are matched exactly, scheme and port included. `https://app.example.com` and
`https://app.example.com:8443` are two different origins, and so are the `http` and `https`
spellings of the same host.

## The combination that is refused

A wildcard origin combined with credentials is refused as a configuration error rather than
served.

```go
// This does not start. muzak.ErrCORSWildcardCredentials is reported when the
// application is built.
muzak.CORSOptions{
    AllowedOrigins:   []string{"*"},
    AllowCredentials: true,
}
```

Browsers reject that pairing anyway, so accepting it here would only hide the mistake until
it reached a browser and then look like a bug in the framework.

## Deciding dynamically

```go
muzak.CORSOptions{
    AllowOriginFunc: func(origin string) bool {
        return strings.HasSuffix(origin, ".example.com")
    },
    AllowCredentials: true,
}
```

It is consulted only when `AllowedOrigins` does not already allow the origin, and it runs on
every cross-origin request, so it must be cheap and free of side effects.

Be careful with suffix matching. `strings.HasSuffix(origin, "example.com")` without the dot
also matches `https://notexample.com`. Prefer an exact list, or parse the origin and compare
the host.

## Exposing response headers

A browser lets a script read only a small safelist of response headers unless the policy
says otherwise. If your clients read the request identifier or the rate limit budget, name
those headers:

```go
ExposedHeaders: []string{
    muzak.HeaderRequestID,
    muzak.HeaderRateLimitLimit,
    muzak.HeaderRateLimitRemaining,
    muzak.HeaderRateLimitReset,
},
```

## Where CORS sits in the chain

CORS runs after any middleware installed with `App.Use` and before the documentation routes
and the router, so it covers the API, `/openapi.json` and any documentation page alike.

```
RequestID → SecurityHeaders → Recovery → AccessLog → your middleware → CORS → /docs and /openapi.json → routes
```

A preflight `OPTIONS` is answered by the CORS middleware. Routes also answer `OPTIONS`
automatically with an `Allow` header when nothing else does, which is a different mechanism
for a different question: `Allow` says which methods a path serves, and the CORS headers say
which a cross-origin caller may use.

## What CORS does not cover

**A WebSocket handshake.** It is not subject to the same-origin policy and is never
preflighted, which is what makes cross-site hijacking possible in the first place.
`AppOptions.CORS` has no bearing on it. The separate check is
`WSOptions.AllowedOrigins`, and a cross-origin handshake is refused until it names one. See
[WebSockets](/docs/realtime/websockets).

**An event stream** is covered, because an `EventSource` is an ordinary request subject to
the same-origin policy and to CORS like any other. See
[Server-Sent Events](/docs/realtime/server-sent-events).

**A server-to-server client.** CORS is a browser mechanism. `curl`, a Go client and a
mobile app ignore it entirely, so it is not an access control: it decides what a *browser*
will let a page read, and nothing more. Authorization is a
[guard or a provider](/docs/security/authorization).

## Testing it

```go
func TestCORSAllowsTheApp(t *testing.T) {
    client := testclient.New(t, buildApp())

    res := client.Options("/items/",
        testclient.Header("Origin", "https://app.example.com"),
        testclient.Header("Access-Control-Request-Method", "POST"))

    res.AssertHeader("Access-Control-Allow-Origin", "https://app.example.com")
}

func TestCORSDeniesAnyoneElse(t *testing.T) {
    client := testclient.New(t, buildApp())

    res := client.Get("/items/", testclient.Header("Origin", "https://evil.example"))

    if got := res.Header.Get("Access-Control-Allow-Origin"); got != "" {
        t.Errorf("Access-Control-Allow-Origin = %q, want no header at all", got)
    }
}
```

## Where to go next

[Safe Defaults](/docs/security/safe-defaults) covers the rest of what is denied
until it is configured, and [TLS](/docs/security/tls) covers serving the origins
above over HTTPS.


--------------------------------------------------------------------------------
title: "TLS"
description: "Serve HTTPS from a certificate pair or a tls.Config, and know what changes in the rest of the application once you do."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/security/tls"
--------------------------------------------------------------------------------

# TLS

`App.Run` serves TLS whenever `ServerOptions` supplies a certificate pair or a
`*tls.Config`. `ServerOptions` is embedded in `AppOptions`, so both are set inline.

## From a certificate on disk

::code-group

```go [cmd/main.go]
app := muzak.New(muzak.AppOptions{
    Title:    "Awesome API",
    Version:  "1.0.0",
    Addr:     ":8443",
    CertFile: settings.TLSCertFile,
    KeyFile:  settings.TLSKeyFile,
})

log.Fatal(app.RunSignals())
```

```bash [.env]
ADDR=:8443
TLS_CERT_FILE=/etc/ssl/certs/api.example.com.pem
TLS_KEY_FILE=/etc/ssl/private/api.example.com.key
```

```go [core/config.go]
type Settings struct {
    Addr        string `env:"ADDR" default:":8080"`
    TLSCertFile string `env:"TLS_CERT_FILE"`
    TLSKeyFile  string `env:"TLS_KEY_FILE"`
}
```

::

Leaving both empty serves plain HTTP, which is what a development run and a deployment
behind a TLS-terminating proxy both want.

## From a tls.Config

A `*tls.Config` covers everything a certificate pair cannot: several certificates, client
certificates, a pinned minimum version, or a certificate that comes from somewhere other
than a file.

```go
app := muzak.New(muzak.AppOptions{
    Title: "Awesome API",
    Addr:  ":8443",
    TLSConfig: &tls.Config{
        MinVersion: tls.VersionTLS13,
        Certificates: []tls.Certificate{cert},
    },
})
```

### Mutual TLS

```go
pool := x509.NewCertPool()
pool.AppendCertsFromPEM(caPEM)

app := muzak.New(muzak.AppOptions{
    Title: "Internal API",
    Addr:  ":8443",
    TLSConfig: &tls.Config{
        MinVersion:   tls.VersionTLS13,
        Certificates: []tls.Certificate{serverCert},
        ClientCAs:    pool,
        ClientAuth:   tls.RequireAndVerifyClientCert,
    },
})
```

The verified client certificate is on the request, so a provider can turn it into an
identity like any other credential:

```go
func GetClientIdentity(ctx *muzak.Context) (ClientIdentity, error) {
    state := ctx.Request().TLS
    if state == nil || len(state.PeerCertificates) == 0 {
        return ClientIdentity{}, muzak.Unauthorized("a client certificate is required")
    }
    return ClientIdentity{CommonName: state.PeerCertificates[0].Subject.CommonName}, nil
}
```

### A certificate that renews itself

`GetCertificate` is consulted per handshake, which is what an ACME client or a certificate
that rotates on disk needs.

```go
app := muzak.New(muzak.AppOptions{
    Title: "Awesome API",
    Addr:  ":8443",
    TLSConfig: &tls.Config{
        MinVersion:     tls.VersionTLS13,
        GetCertificate: certificates.Current,
    },
})
```

Whatever keeps `certificates` up to date is a resource with a start and a stop, so give it
a `muzak.Lifecycle` and publish it. See
[Lifecycle](/docs/getting-started/lifecycle).

## What changes once you serve HTTPS

**Cookies.** Set `Secure: true` on every cookie, so a browser refuses to send it over plain
HTTP.

```go
ctx.SetCookie(&http.Cookie{
    Name:     "session_id",
    Value:    session,
    Path:     "/",
    HttpOnly: true,
    SameSite: http.SameSiteLaxMode,
    Secure:   true,
    MaxAge:   3600,
})
```

**Origins.** The `https` and `http` spellings of a host are different origins. Update
`CORSOptions.AllowedOrigins` and `WSOptions.AllowedOrigins` accordingly.

**WebSocket URLs.** A page served over `https` must dial `wss`, not `ws`.

**The `Servers` list.** Point the generated document at the address clients actually use:

```go
Servers: []muzak.Server{
    {URL: "https://api.example.com", Description: "production"},
},
```

## Terminating TLS somewhere else

A load balancer, an ingress controller or a reverse proxy commonly holds the certificate
and speaks plain HTTP to the service behind it. Muzak then serves HTTP, and two things
need saying explicitly:

- **Name the proxy** in `ClientIPOptions.TrustedProxies`, or every request is attributed to
  the proxy's address rather than the client's. See
  [Behind a Proxy](/docs/deployment/behind-a-proxy).
- **Keep cookies `Secure`** anyway. The browser leg of the connection is the one that
  matters, and it is HTTPS.

## Strict transport security

Muzak's `SecurityHeaders` middleware sets `X-Content-Type-Options`, `X-Frame-Options` and a
referrer policy. It does not set `Strict-Transport-Security`, because that header commits
every future visitor's browser to HTTPS for the duration it names, and a service that is not
yet reachable over TLS on every hostname it answers to would lock itself out.

Add it deliberately once you are sure:

```go
func HSTS(maxAge time.Duration) muzak.Middleware {
    value := "max-age=" + strconv.Itoa(int(maxAge.Seconds())) + "; includeSubDomains"
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if r.TLS != nil {
                w.Header().Set("Strict-Transport-Security", value)
            }
            next.ServeHTTP(w, r)
        })
    }
}
```

```go
app.Use(HSTS(365 * 24 * time.Hour))
```

The `r.TLS != nil` check keeps the header off a plain-HTTP response, where it means nothing
and where a redirect is the more useful answer. Behind a terminating proxy the check has to
read whatever header the proxy sets instead.

## Development certificates

```bash
go run filippo.io/mkcert@latest -install
go run filippo.io/mkcert@latest localhost 127.0.0.1
```

```bash [.env]
ADDR=:8443
TLS_CERT_FILE=./localhost+1.pem
TLS_KEY_FILE=./localhost+1-key.pem
```

Tests need none of this. The test client serves the application in-process over an
in-memory network, so there is no socket and nothing to encrypt. See
[Testing](/docs/fundamentals/testing).

## Where to go next

[Behind a Proxy](/docs/deployment/behind-a-proxy) covers running with TLS
terminated in front of you, and
[Server Configuration](/docs/deployment/server-configuration) covers the rest of
the listener.


--------------------------------------------------------------------------------
title: "Server Configuration"
description: "The listener, its timeouts, the run methods, and a shutdown that drains requests before it releases the resources they were using."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/deployment/server-configuration"
--------------------------------------------------------------------------------

# Server Configuration

`AppOptions` embeds `ServerOptions`, so the listener is configured in the same literal as
everything else.

```go [cmd/main.go]
app := muzak.New(muzak.AppOptions{
    Title:   "Awesome API",
    Version: "1.0.0",
    Addr:    settings.Addr,

    ReadHeaderTimeout: 5 * time.Second,
    ReadTimeout:       30 * time.Second,
    WriteTimeout:      30 * time.Second,
    IdleTimeout:       120 * time.Second,
    ShutdownTimeout:   15 * time.Second,
    MaxHeaderBytes:    1 << 20,
})
```

Every one of those already has that value by default. They are written out here to show
what they are, not because they need setting.

## Timeouts

| Field | Default | Bounds |
|---|---|---|
| `ReadHeaderTimeout` | 5s | How long a client may take to send the request headers |
| `ReadTimeout` | 30s | How long a client may take to send the headers and body together |
| `WriteTimeout` | 30s | How long a handler may take to write its response |
| `IdleTimeout` | 120s | How long a keep-alive connection may sit unused before it is closed |
| `ShutdownTimeout` | 15s | How long a graceful shutdown waits for in-flight requests |
| `MaxHeaderBytes` | 1 MiB | The size of the request header block |

Every timeout is non-zero on purpose. An `http.Server` left with the standard library's
zero values holds a connection open indefinitely, which is all a slow-loris client needs to
exhaust the connection budget.

Setting one to a negative number disables it, which should be reserved for a server behind
a proxy that enforces its own.

WebSocket routes and event streams manage their own deadlines. A stream is a response that
does not end, so the listener's write deadline is cleared for it and replaced with a
deadline per event; the read deadline goes too, since it would cancel the request and blame
the client. See [Server-Sent Events](/docs/realtime/server-sent-events).

## The listen address

```go
Addr: ":8080"          // the default
Addr: "127.0.0.1:8080" // loopback only
Addr: ":0"             // any free port, which is what a test wants
```

`app.Addr()` reports the address actually being listened on, which is how a test that asked
for `:0` discovers the port it was given. It returns an empty string before the server has
started.

`app.Config()` returns the fully defaulted options the application was built with, which is
the reliable way to read a value after `New` has filled in the blanks.

## Running

| Method | Stops when |
|---|---|
| `Run()` | The server fails, or `Shutdown` is called from elsewhere |
| `RunContext(ctx)` | `ctx` is cancelled, or the server fails |
| `RunSignals()` | `SIGINT` or `SIGTERM` arrives, or the server fails |

```go [cmd/main.go]
if err := app.RunSignals(); err != nil {
    log.Fatal(err)
}
```

`RunSignals` is what a `main` function usually wants: `SIGINT` and `SIGTERM` are what a
terminal, a container runtime and an init system all send to ask a process to stop.

All three build the application first, so a configuration error is reported before any
socket is opened, then start the lifecycle components, then listen. A shutdown asked for
returns nil, because stopping on request is the expected outcome rather than a failure.

```
14:32:07.482 INFO  [Server]        Starting Muzak application...
14:32:07.483 INFO  [Server]        Starting 2 lifecycle components in parallel: item-store, ml-model
14:32:07.501 INFO  [Server]        Started "item-store" (18ms)
14:32:07.512 INFO  [Server]        Started "ml-model" (29ms)
14:32:07.513 INFO  [Server]        All lifecycle components ready (29ms total)
14:32:07.514 INFO  [Server]        Listening on :8080
```

`RunContext` is the one to reach for when something else decides the process should stop:

```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer stop()

if err := app.RunContext(ctx); err != nil {
    log.Fatal(err)
}
```

## Shutting down

```go
if err := app.Shutdown(context.Background()); err != nil {
    log.Printf("shutdown: %v", err)
}
```

The sequence is the one that matters:

1. Open WebSocket connections are closed with `1001 Going Away`, and open event streams are
   ended. Both are tracked by the application precisely so this can happen: a hijacked
   connection is no longer one `net/http` knows about, and a streaming handler would
   otherwise make the drain wait out its whole deadline once per stream.
2. The server stops accepting new connections and drains its in-flight requests, giving up
   after `ShutdownTimeout` and closing whatever remains.
3. The lifecycle components are stopped.

Components stay usable until the very end, because pulling a database connection out from
under a request that is still running would turn an orderly shutdown into a burst of
errors.

`Shutdown` is safe to call more than once and from more than one goroutine; only the first
call does the work. The context argument can cut the wait short; pass `context.Background()`
to use the configured timeout alone. Calling it on a server that was never started returns
nil.

```
14:32:41.902 INFO  [Server]        Shutting down, waiting for in-flight requests...
14:32:41.903 INFO  [Server]        Closed 3 websocket connections
14:32:41.911 INFO  [Server]        Stopping 2 lifecycle components
14:32:41.913 INFO  [Server]        Stopped "ml-model" (2ms)
14:32:41.918 INFO  [Server]        Stopped "item-store" (5ms)
14:32:41.918 INFO  [Server]        Stopped
```

Give the orchestrator more grace than `ShutdownTimeout`, or it kills the process part way
through the drain. The default of 15 seconds is longer than some platforms allow, so this
is a setting to check rather than inherit:

| Platform | Grace after `SIGTERM` | Where it is set |
|---|---|---|
| Cloud Run | about 10s | fixed, so `ShutdownTimeout` must come down |
| Kubernetes | 30s | `terminationGracePeriodSeconds` |
| ECS | 30s | `stopTimeout` |

Leave room after the drain for anything the application flushes on the way out. A service
that publishes to a message broker has to get its buffered messages away once the last
request is answered, and that time comes out of the same grace period.

## Serving it yourself

`App` implements `http.Handler`, so it can be mounted inside a server you build.

```go
if err := app.Build(); err != nil {
    return err
}
if err := app.StartLifecycle(ctx); err != nil {
    return err
}
defer app.StopLifecycle(context.Background())

server := &http.Server{
    Addr:              ":8080",
    Handler:           app,
    ReadHeaderTimeout: 5 * time.Second,
}
return server.ListenAndServe()
```

Two things become yours when you do that: the timeouts, which Muzak would otherwise have
set for you, and the lifecycle, which nothing else will start. A build failure is reported
as a `500` for every request with the reason logged once, which is why `Build` is worth
calling first.

The same shape mounts the application under a path in a larger server:

```go
mux := http.NewServeMux()
mux.Handle("/api/", http.StripPrefix("/api", app))
```

Mounting under a prefix with `muzak.WithPrefix` is usually better, because the generated
document then carries the real paths.

## A base context

```go
app := muzak.New(muzak.AppOptions{
    Title: "Awesome API",
    BaseContext: func(l net.Listener) context.Context {
        return context.WithValue(context.Background(), tenantKey{}, tenant)
    },
})
```

Every request context derives from what this returns. When it is nil, requests derive from
`context.Background`.

## Checking the configuration before deploying

```go
func TestAppBuilds(t *testing.T) {
    if err := buildApp().Build(); err != nil {
        t.Fatalf("Build = %v", err)
    }
}
```

`Build` reports every problem at once: duplicate routes, unbindable input types, path
parameters no field binds, duplicate operation identifiers, malformed prefixes, a
`MaxConnections` set anywhere but the application, a missing static directory. One test
keeps all of them out of a deployment.

## In production

A short list of things worth deciding before the first deployment:

```go
app := muzak.New(muzak.AppOptions{
    Title:   settings.AppName,
    Version: buildVersion,
    Addr:    settings.Addr,

    // A generated client should point at the real host.
    Servers: []muzak.Server{{URL: "https://api.example.com", Description: "production"}},

    // Behind a load balancer, or every request is attributed to the balancer.
    ClientIP: muzak.ClientIPOptions{TrustedProxies: settings.TrustedProxies},

    // A health check polled every second should not drown out real traffic.
    AccessLogOptions: muzak.AccessLogOptions{SkipPaths: []string{"/healthz"}},
})
```

- **Logging** picks JSON on its own when the output is not a terminal, so a container gets
  parseable logs with no configuration. Force it with
  `LoggerOptions{Format: muzak.LogFormatJSON}` if something in between confuses the
  detection.
- **A rate limit policy**, with a shared storage if the service runs more than once. See
  [Rate Limiting](/docs/techniques/rate-limiting).
- **`DisableDocs`** if the deployment must not describe itself, which stops the document
  as well as any page. Leaving `DocsUI` unset already means no page is served; this is
  for the document too. It is worth generating in CI either way, so a breaking change
  shows up in a diff. See [OpenAPI](/docs/fundamentals/openapi).
- **A liveness route**, routable and out of the document:

```go
r.Get("/healthz", handlers.Health,
    muzak.Summary("Liveness probe"),
    // Routable, but left out of the documentation.
    muzak.Hidden(),
    // A monitor polling every second is the one client that should never be
    // told to slow down.
    muzak.SkipRateLimit())
```

## Building the binary

```bash
go build -o awesome-api ./cmd
```

```dockerfile [Dockerfile]
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/awesome-api ./cmd

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/awesome-api /awesome-api
EXPOSE 8080
ENTRYPOINT ["/awesome-api"]
```

Muzak has no third-party dependencies, so `CGO_ENABLED=0` and a static base image are
enough. A frontend embedded with `//go:embed` travels inside the same binary, so the image
holds one file. See [Static Files and Frontends](/docs/techniques/static-files).

Make sure the runtime forwards `SIGTERM` to the process rather than to a shell wrapper, or
`RunSignals` never sees it and the drain never happens.

## Where to go next

[Behind a Proxy](/docs/deployment/behind-a-proxy) covers what changes when
something sits in front of the service, and
[Lifecycle](/docs/getting-started/lifecycle) covers the components start-up and
shutdown are sequencing.


--------------------------------------------------------------------------------
title: "Behind a Proxy"
description: "Which address a request is attributed to, why no forwarding header is believed until a proxy is named, and what else changes with a load balancer in front."
version: "0.2.7"
last_updated: "2026-09-03T13:19:54.000Z"
source: "https://muzak.dev/docs/0.2.7/deployment/behind-a-proxy"
--------------------------------------------------------------------------------

# Behind a Proxy

Muzak attributes a request to the peer that opened the connection, and ignores every
forwarding header. That is the only safe default: a forwarding header is a request header
like any other, and a server that believes one without knowing who wrote it lets any client
claim any address it likes.

Behind a proxy the default is wrong in the other direction, since every request then appears
to come from the proxy. Naming the proxy is what makes the header believable.

## Naming the proxy

::code-group

```go [cmd/main.go]
app := muzak.New(muzak.AppOptions{
    Title: settings.AppName,
    Addr:  settings.Addr,
    // Which address a request is attributed to. Nothing is believed from a
    // header until the proxy that wrote it is named here.
    ClientIP: muzak.ClientIPOptions{TrustedProxies: settings.TrustedProxies},
})
```

```go [core/config.go]
type Settings struct {
    // TrustedProxies lists the proxies whose X-Forwarded-For header is
    // believed, as a comma-separated list of addresses or CIDR prefixes. It is
    // empty by default, so every request is attributed to the peer that made
    // it: a forwarding header is written by whatever sent the request, and
    // believing one from an unknown sender hands every client the ability to
    // choose which budget it spends.
    TrustedProxies []string `env:"TRUSTED_PROXIES"`
}
```

```bash [.env]
# Proxies whose X-Forwarded-For header is believed, as a comma-separated list of
# addresses or CIDR prefixes. Leave it empty when nothing sits in front of this
# service.
TRUSTED_PROXIES=10.0.0.0/8,172.16.0.0/12
```

::

Entries are plain addresses (`10.1.2.3`) or CIDR prefixes (`10.0.0.0/8`), and both address
families are accepted. An entry that cannot be parsed is reported when the application is
built, rather than quietly widening or narrowing the policy.

The header is consulted **only** for a request whose peer is trusted. A request arriving
directly from the internet with an `X-Forwarded-For` of its own choosing is attributed to
the address it actually came from.

## A header other than X-Forwarded-For

```go
muzak.ClientIPOptions{
    TrustedProxies: []string{"10.0.0.0/8"},
    Header:         "CF-Connecting-IP",
}
```

`DefaultForwardedHeader` is `X-Forwarded-For`. Set `Header` to whatever your proxy actually
writes, such as `CF-Connecting-IP` or `X-Real-IP`.

## Reading the address

```go
ctx.ClientIP()   // the address as a string, normalised
ctx.ClientAddr() // the same as a net/netip.Addr, for comparing against a prefix
```

```go
addr := ctx.ClientAddr()
if !addr.IsValid() {
    // The connection has no address that can be parsed, which happens on a
    // listener that is not addressed by IP, such as a Unix socket.
}
if office.Contains(addr) {
    // ...
}
```

The result is normalised, so an address written in IPv4-in-IPv6 form and the same address
written plainly are one value rather than two. That is what stops a client from being
counted twice, or from evading a count, by rewriting its own address.

`ClientIP` returns the empty string when the connection has no parsable address. Code that
keys on the result has to handle that, which is what `muzak.IPTracker` does by refusing the
request rather than counting every such request under one shared key.

## What depends on getting this right

| Feature | Uses the client address for |
|---|---|
| Rate limiting | `IPTracker` and `IPPrefixTracker` key the budget on it |
| WebSockets | `MaxConnectionsPerIP` bounds one client's share of the process |
| Server-sent events | `MaxStreamsPerIP` does the same for streams |
| Anything you write | Deny lists, audit logs, geo decisions |

With an untrusted proxy in front and nothing named in `TrustedProxies`, every request
carries the proxy's address, so the whole world shares one rate limit budget and one
connection allowance. With a *wrongly* trusted header, any client picks its own budget by
writing a header. Both failure modes are why this is configuration rather than a guess.

## Rate limiting behind a proxy

Once the address is right, the tracker follows.

```go
muzak.WithRateLimit(muzak.RateLimitOptions{
    Tracker: muzak.IPPrefixTracker(32, 64),
    Quotas: []muzak.Quota{
        {Name: "short", Window: time.Second, Limit: 3},
        {Name: "long", Window: time.Minute, Limit: 100},
    },
})
```

An IPv6 `/64` is the block size most providers hand out, so keying on the exact address
gives a client holding one a fresh budget on every request. `IPPrefixTracker(32, 64)` keeps
IPv4 exact and collapses IPv6 to the allocation it came from. See
[Rate Limiting](/docs/techniques/rate-limiting).

A service running more than once needs a storage the processes share, or a limit of a
hundred a minute becomes a hundred a minute per process.

## TLS terminated in front

Common, and it changes three things:

- The service speaks plain HTTP, so `CertFile`, `KeyFile` and `TLSConfig` stay empty.
- Cookies still need `Secure: true`, because the browser leg of the connection is HTTPS and
  that is the leg the attribute governs.
- `Strict-Transport-Security`, if you set it, cannot be gated on `r.TLS != nil`, because
  the request Muzak sees is not the encrypted one. Gate it on whatever header the proxy
  writes, or set it at the proxy.

See [TLS](/docs/security/tls).

## Streaming through a proxy

An event stream is the request most likely to be broken by something in the middle. Muzak
sets `Cache-Control: no-cache, no-transform` and `X-Accel-Buffering: no`, and writes a
keepalive comment every `SSEOptions.KeepAlive`, so a proxy does not close a connection it
believes to be idle.

Check the proxy's own settings too. `proxy_buffering off` and a read timeout longer than
the keepalive are what nginx needs; the equivalents exist elsewhere. A WebSocket needs the
proxy configured to upgrade the connection at all.

## Request identifiers

Muzak generates an identifier per request and ignores an inbound `X-Request-Id`, because
an attacker-controlled identifier is an attacker-controlled log field.

Where a trusted proxy assigns the identifier and you want the two logs to agree, accept it:

```go
app := muzak.New(muzak.AppOptions{
    Title:                "Awesome API",
    TrustRequestIDHeader: true,
})
```

An inbound value is honoured only if it parses as a UUID, so it can never carry newlines or
control characters into a log line. Turn it on only where nothing untrusted can reach the
service directly, since the header would otherwise come straight from the client.

## Body limits in two places

The proxy has its own body limit, and so does Muzak. Set the proxy's at or above the
route's, or a large upload is refused with the proxy's error page rather than the
application's `413` and its error envelope. See
[File Uploads](/docs/techniques/file-uploads).

## Checking it works

```bash
curl -s http://localhost:8080/whoami -H 'X-Forwarded-For: 203.0.113.9'
```

```go
type WhoAmIOut struct {
    ClientIP string `json:"client_ip"`
}

r.Get("/whoami", func(ctx *muzak.Context, _ muzak.Empty) (WhoAmIOut, error) {
    return WhoAmIOut{ClientIP: ctx.ClientIP()}, nil
}, muzak.Hidden())
```

Called directly, that answers with your own address whatever the header says. Called
through a proxy named in `TrustedProxies`, it answers with what the proxy reported. Those
two results are the whole configuration, and they are worth checking once per deployment.

Remove the route afterwards, or leave it `Hidden` and behind a guard.

## Where to go next

[Server Configuration](/docs/deployment/server-configuration) covers the listener
itself, and [Rate Limiting](/docs/techniques/rate-limiting) covers the feature
that depends on this most.
