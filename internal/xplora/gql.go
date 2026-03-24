package xplora

// GraphQL query and mutation strings for the Xplora API.
// These are derived from the pyxplora_api open-source Python library.
const (
	MutationSignIn = `
mutation signInWithEmailOrPhone(
  $countryPhoneNumber: String
  $phoneNumber: String
  $password: String!
  $emailAddress: String
  $client: ClientType!
  $userLang: String!
  $timeZone: String!
) {
  signInWithEmailOrPhone(
    countryPhoneNumber: $countryPhoneNumber
    phoneNumber: $phoneNumber
    password: $password
    emailAddress: $emailAddress
    client: $client
    userLang: $userLang
    timeZone: $timeZone
  ) {
    id
    token
    refreshToken
    expireDate
    user {
      id
    }
    w360 {
      token
      secret
    }
  }
}`

	MutationRefreshToken = `
mutation RefreshToken($uid: String!, $refreshToken: String!) {
  refreshToken(uid: $uid, refreshToken: $refreshToken) {
    id
    token
    refreshToken
    expireDate
    valid
  }
}`

	// MutationSetFCMToken registers an FCM token with the Xplora backend.
	// terminalType and isAndroid are hardcoded in the mutation body as per pyxplora_api.
	MutationSetFCMToken = `
mutation setFCMToken(
  $clientId: String!
  $fcmToken: String!
  $manufacturer: String
  $brand: String
  $model: String
  $osVer: String
  $userLang: String!
  $timeZone: String
) {
  setFCMToken(
    clientId: $clientId
    fcmToken: $fcmToken
    terminalType: ANDROID
    manufacturer: $manufacturer
    brand: $brand
    model: $model
    osVer: $osVer
    userLang: $userLang
    timeZone: $timeZone
    isAndroid: true
  )
}`

	MutationSendChatText = `
mutation SendChatText($uid: String!, $text: String!) {
  sendChatText(uid: $uid, text: $text)
}`

	// MutationSetReadChatMsg marks a chat message as read.
	// Note: the API field is setReadChatMsg (no trailing M).
	MutationSetReadChatMsg = `
mutation SetReadChatMsg($uid: String!, $msgId: String, $id: String) {
  setReadChatMsg(uid: $uid, msgId: $msgId, id: $id)
}`

	// QueryChats fetches paginated messages for a watch.
	// data is a JSON blob containing type-specific message content.
	// create is the Unix timestamp (milliseconds).
	// sender.id identifies who sent the message (child vs parent).
	// $msgId optionally filters to messages after a given ID.
	QueryChats = `
query Chats($uid: String!, $offset: Int, $limit: Int, $msgId: String) {
  chatsNew(uid: $uid, offset: $offset, limit: $limit, msgId: $msgId) {
    offset
    limit
    list {
      id
      msgId
      type
      sender {
        id
      }
      data
      create
    }
  }
}`

	// QueryWatches returns all child watches linked to a parent account.
	// user.id is the child's user ID used as the UID for chatsNew.
	QueryWatches = `
query Watches($uid: String) {
  watches(uid: $uid) {
    id
    name
    user {
      id
      name
    }
  }
}`

	QueryReadMyInfo = `
query ReadMyInfo {
  readMyInfo {
    id
    name
  }
}`
)
