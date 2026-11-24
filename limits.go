package aemeath

// SetMaxBodyBytes sets the limit for JSON request bodies and urlencoded forms
// of typed routes (1 MiB by default). Larger bodies get 413. Call it before
// the server starts; it panics once the app has served a request.
func (a *App) SetMaxBodyBytes(n int64) {
	a.mustNotBeServing("SetMaxBodyBytes")
	a.maxBody = &n
}

// SetMaxMultipartBytes sets the limit for a whole multipart/form-data request
// body of typed routes (32 MiB by default). Larger bodies get 413. Call it
// before the server starts; it panics once the app has served a request.
func (a *App) SetMaxMultipartBytes(n int64) {
	a.mustNotBeServing("SetMaxMultipartBytes")
	a.maxMultipart = &n
}

// SetMaxRequestBytes caps every request body, including the ones a handler
// reads itself from c.R.Body, so a client cannot stream gigabytes into a
// route that forgot a limit. 0 (the default) means the larger of the JSON and
// multipart limits; a negative value turns the cap off. MaxBodySize on a
// group or route replaces it. Call it before the server starts; it panics
// once the app has served a request.
func (a *App) SetMaxRequestBytes(n int64) {
	a.mustNotBeServing("SetMaxRequestBytes")
	a.maxRequest = &n
}

// SetMaxValidationErrors sets the most validation errors one request reports
// (100 by default). Validation stops once it is reached: without a limit, a
// 1 MiB body of 349,517 empty objects made the server build and send 349,517
// errors, a 25 MB response. 0 means no limit. Call it before the server
// starts; it panics once the app has served a request.
func (a *App) SetMaxValidationErrors(n int) {
	a.mustNotBeServing("SetMaxValidationErrors")
	a.maxValidation = &n
}

// The limits in force: the app's own when set, else the package variables
// (deprecated, but an app that sets nothing must behave as before).

func (a *App) bodyBytes() int64 {
	if a != nil && a.maxBody != nil {
		return *a.maxBody
	}
	return DefaultMaxBodyBytes
}

func (a *App) multipartBytes() int64 {
	if a != nil && a.maxMultipart != nil {
		return *a.maxMultipart
	}
	return DefaultMaxMultipartBytes
}

func (a *App) requestBytes() int64 {
	if a != nil && a.maxRequest != nil {
		return *a.maxRequest
	}
	return DefaultMaxRequestBytes
}

func (a *App) validationErrors() int {
	if a != nil && a.maxValidation != nil {
		return *a.maxValidation
	}
	return MaxValidationErrors
}

// BodyLimits returns the body limits of the request's app: for JSON bodies
// and urlencoded forms, and for multipart forms. Middleware that reads a
// form itself (as csrf does) should apply the same limits as typed routes.
func (c *Ctx) BodyLimits() (body, multipart int64) {
	return c.app.bodyBytes(), c.app.multipartBytes()
}
