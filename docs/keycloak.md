# Sign-in with Keycloak

Station Finder can let only the people you register in Keycloak use the app. Clicking
**Sign in** opens your Keycloak login page; after a successful login Keycloak sends the
user back to the app, which keeps them signed in for 12 hours. When those 12 hours end,
the app checks with Keycloak again. If the Keycloak session is still active this
happens silently; otherwise the user enters their password again.

- **Users** are created and removed in Keycloak. There is no user list in the app.
- **Admins** are users with the `admin` role. They can fix station locations and re-run
  geocoding. Everyone else can search.
- **Tokens:** the app server talks to Keycloak directly; the browser only gets an
  HttpOnly session cookie, never a token.

The steps below use the Keycloak 26 admin console (tested with 26.8). Replace:

| Placeholder | Meaning |
|---|---|
| `https://keycloak.example.com` | your Keycloak address |
| `https://stations.example.com` | the address users open Station Finder at |

---

## Part 1 – Set up Keycloak (once)

Open `https://keycloak.example.com/admin/` and sign in as the Keycloak administrator.

### 1. Create a realm for the app
A realm is a separate set of users. Using one just for Station Finder keeps its users
apart from anything else on your Keycloak.

1. In the left sidebar, click **Manage realms → Create realm**.
2. **Realm name:** `station-finder` → **Create**.
3. Make sure the realm selector at the top of the sidebar shows **station-finder** for
   all the following steps.
4. **Realm settings → General → Display name:** `Station Finder` → **Save**. This is the
   title shown on the login page.
5. **Realm settings → Login:**
   - **User registration: Off** (the default). Nobody can create their own account; only
     you can add users.
   - Optional: **Forgot password: On**. Users can then reset their own password by email,
     which needs the **Email** tab (SMTP server) set up first.
   - Optional: **Remember me: On**.
6. **Realm settings → Security defenses → Brute force detection:** choose
   **Lockout temporarily** → **Save**. Repeated wrong passwords then lock the account
   for a while.

### 2. Create the client for Station Finder
1. **Clients → Create client**.
2. **General settings:**
   - **Client type:** OpenID Connect
   - **Client ID:** `station-finder`
   - **Name:** `Station Finder`
   - Click **Next**.
3. **Capability config:**
   - **Client authentication: On.** This makes it a confidential client with a secret.
   - **Authorization:** Off.
   - **Authentication flow:** tick only **Standard flow**. Untick **Direct access grants**.
   - **Require PKCE:** On (the app always uses PKCE).
   - Click **Next**.
4. **Login settings:**

   | Field | Value |
   |---|---|
   | Root URL | `https://stations.example.com` |
   | Home URL | `https://stations.example.com` |
   | Valid redirect URIs | `https://stations.example.com/auth/callback` |
   | Valid post logout redirect URIs | `https://stations.example.com/*` |
   | Web origins | `https://stations.example.com` |

   Click **Save**.
5. Open the **Credentials** tab and copy the **Client Secret**. You need it in Part 2.
   Treat it like a password.

### 3. Create the admin role
1. **Clients → station-finder → Roles → Create role**.
2. **Role name:** `admin`, **Description:** `Can fix station locations` → **Save**.

---

## Part 2 – Connect Station Finder to Keycloak (once)

1. Check the issuer. Open
   `https://keycloak.example.com/realms/station-finder/.well-known/openid-configuration`
   in a browser. The `"issuer"` value at the top is what the app needs, character for
   character:
   - It normally looks like `https://keycloak.example.com/realms/station-finder`.
   - Keycloak 16 and older add `/auth` before `/realms`.
   - If it starts with `http://` although your Keycloak uses HTTPS, Keycloak doesn't
     know it runs behind a proxy. Start Keycloak with
     `KC_HOSTNAME=https://keycloak.example.com` and `KC_PROXY_HEADERS=xforwarded`.
2. Set these environment variables on the app:
   - **Dokploy:** the application's **Environment** tab.
   - **docker compose:** the `.env` file next to `docker-compose.yml`.
   ```
   OIDC_ISSUER=https://keycloak.example.com/realms/station-finder
   OIDC_CLIENT_ID=station-finder
   OIDC_CLIENT_SECRET=<the Client Secret from Part 1, step 2>
   PUBLIC_URL=https://stations.example.com
   ```
   Optional:
   - `OIDC_ADMIN_ROLE`: default `admin`.
   - `OIDC_REQUIRED_ROLE`: see "Sharing a realm with other apps" below.
   - `SESSION_SECRET`: 32+ random characters. If empty, one is generated and kept in
     the data volume.
3. Redeploy (Dokploy: **Deploy**; compose: `docker compose up -d`). The app log should
   show:
   ```
   sign-in with https://keycloak.example.com/realms/station-finder (client "station-finder", admin role "admin")
   ```
   It must not be followed by `WARNING: sign-in server not reachable yet`. The app
   server itself has to reach the Keycloak address, because it calls Keycloak directly.
4. Open `https://stations.example.com`. You should see **Sign in**, which takes you to the
   Keycloak login page.

---

## Part 3 – Register a user (repeat for each person)

1. **Users → Add user**:
   - **Email verified:** On
   - **Username:** e.g. `nimal`
   - **Email**, **First name**, **Last name.** Fill all three, otherwise Keycloak asks
     the user to complete their profile on first login. The app shows the full name in
     its header.
   - Click **Create**.
2. **Credentials** tab → **Set password**:
   - Enter the **Password** and **New password confirmation**.
   - Leave **Temporary: On** so the user must choose their own password at first login.
   - Click **Save** and confirm.
3. **Only for admins:** **Role mapping** tab → **Assign role → Client roles**. Search for
   `admin`, tick the row whose client is **station-finder**, and click **Assign**.
4. Send the person the app address, their username and the temporary password. At
   their first login Keycloak shows **Update password**; after that they land in the app.

**Tip, admins via a group:** create **Groups → Create group** `station-admins`. In the
group's **Role mapping**, use **Assign role → Client roles → station-finder admin**. To
make someone an admin, open the user's **Groups** tab → **Join Group**.

---

## Day-to-day tasks

| Task | In Keycloak (realm station-finder) |
|---|---|
| Reset a forgotten password | Users → user → **Credentials → Reset password** (Temporary On) |
| Make someone an admin | Users → user → **Role mapping → Assign role → Client roles → admin** |
| Remove admin rights | Users → user → **Role mapping** → tick `admin` → **Unassign** |
| Block a user | Users → user → switch **Enabled** off (or **Action → Delete**) |
| End someone's login now | **Sessions** (left sidebar) → ⋮ on the user's session → **Sign out** |

- Role changes apply when the user signs in again (**Sign out**, then **Sign in**).
- A blocked user keeps the app session they already have until it ends, at most 12
  hours later.
- To sign everyone out of the app immediately, change `SESSION_SECRET` and redeploy.

### Sharing a realm with other apps
If you use an existing realm that also holds users of other applications:
1. Create a second client role `user` on the station-finder client (Part 1, step 3).
2. Assign it to every Station Finder user (or to a group they join).
3. Set `OIDC_REQUIRED_ROLE=user` on the app.

Everyone else in the realm then gets "Your account isn't allowed to use Station Finder".

---

## Troubleshooting

| What you see | Fix |
|---|---|
| Keycloak page says **Invalid parameter: redirect_uri** | **Valid redirect URIs** must contain exactly `PUBLIC_URL` + `/auth/callback`. Check `https` vs `http` and that `PUBLIC_URL` has no path. |
| Back in the app: "Sign-in could not be completed. Check the client secret…" | `OIDC_CLIENT_SECRET` is wrong, or **Client authentication** is off. The app log shows the exact error after `code exchange:`. |
| "The sign-in server can't be reached" | The app can't reach `OIDC_ISSUER` (DNS, firewall), or the issuer doesn't match. Look for `issuer did not match` in the app log and copy the issuer from `.well-known/openid-configuration` (Part 2). |
| After **Sign out**, Keycloak asks "Do you want to log out?" or shows **Invalid redirect uri** | Add `https://stations.example.com/*` to **Valid post logout redirect URIs**. |
| An admin gets "You don't have permission to do this." | The `admin` client role isn't assigned, or they haven't signed in again since it was. |
| Keycloak asks a new user for their name or email | Fill **Email**, **First name** and **Last name** on the user (Part 3, step 1). |

## Trying it locally
Run a Keycloak in dev mode, create the realm, client and users as above, but with:
- `http://localhost:8080` as the app address in the client's URLs
- `PUBLIC_URL=http://localhost:8080`
- `OIDC_ISSUER=http://localhost:<keycloak port>/realms/station-finder`

Then build the UI (`cd frontend && npm run build`) and start the backend with those
environment variables. The backend serves the UI with sign-in on at
http://localhost:8080.
