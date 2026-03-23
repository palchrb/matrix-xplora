package xplora

// GraphQL query and mutation strings for the Xplora API.
// These are derived from the pyxplora_api open-source Python library.
const (
	MutationSignIn = `
mutation signInWithEmailOrPhone(
  $countryCode: String
  $phoneNumber: String!
  $password: String!
  $userAgent: String
  $timeZone: String
) {
  signInWithEmailOrPhone(
    countryCode: $countryCode
    phoneNumber: $phoneNumber
    password: $password
    userAgent: $userAgent
    timeZone: $timeZone
  ) {
    id
    token
    refreshToken
    expireDate
    user {
      id
      username
    }
  }
}`

	MutationSetFCMToken = `
mutation setFCMToken(
  $uid: String!
  $key: String!
  $clientId: String!
  $deviceName: String
  $deviceOs: String
  $deviceOsVer: String
  $deviceBrand: String
  $deviceModel: String
  $type: Int
) {
  setFCMToken(
    uid: $uid
    key: $key
    clientId: $clientId
    deviceName: $deviceName
    deviceOs: $deviceOs
    deviceOsVer: $deviceOsVer
    deviceBrand: $deviceBrand
    deviceModel: $deviceModel
    type: $type
  )
}`

	MutationSendChatText = `
mutation SendChatText($uid: String!, $text: String!) {
  sendChatText(uid: $uid, text: $text)
}`

	MutationSetReadChatMsg = `
mutation SetReadChatMsg($uid: String!, $msgId: String!) {
  setReadChatMsgM(uid: $uid, msgId: $msgId)
}`

	QueryChats = `
query Chats($uid: String!, $offset: Int, $limit: Int) {
  chatsNew(uid: $uid, offset: $offset, limit: $limit) {
    offset
    limit
    list {
      msgId
      type
      text
      tm
      status
    }
  }
}`

	QueryWatches = `
query Watches($uid: String) {
  watches(uid: $uid) {
    uid
    name
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
