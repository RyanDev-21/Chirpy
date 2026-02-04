NOTE:: frontend need to make sure the duplicate member_id doesn't have in the member_list
NOTE:: -- need to abstract the business logic between service and worker
//WARNING:still need to work on this

NOTE:: userRoute/endpoints need to be fixed in RESTFUL way .
--users/{user_id}/info --for update name and stuff with PATCH method(maybe for like bios and stuff)
//i diff these too rather than using a single route for all the update
--users/{user_id}/email/change -- for email update(POST)
--users/{user_id}/email/verify -- for email verification(POST)
--users/{user_id}/password --for password update with POST method
-- need to rethink about this while caching and message queue

NOTE:: need to use redis steam for messages cache
-- need to implement function for generate key for chatID (for both public and
private) like usersID:chatID(chatID = otherUserID+userID when private)
-- need to update storing the message id too should only store what returns id
from xadding into redis stream 

