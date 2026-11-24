namespace go api.auth

// One access token per instance. Login and bootstrap exchange it for the
// HttpOnly session cookie; the desktop shell uses neither.
struct LoginReq {
    1: string token (api.body="token", api.vd="len($) > 0")
}

struct AuthResp {
    1: i32 code
    2: string msg
    3: bool authenticated
}

struct MeResp {
    1: i32 code
    2: string msg
    3: bool authenticated
    // true when the caller is the desktop shell
    4: bool desktop
}

// GET that sets the session cookie and answers 303 to "/"; there is no JSON body.
struct BootstrapReq {
    1: string token (api.query="token")
}
