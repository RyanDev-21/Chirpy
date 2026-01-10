package chat

import "RyanDev-21.com/Chirpy/internal/database"


type chatRepo struct{
	quries *database.Queries	
}


type ChatRepo interface{
	any		
}


func NewChatRepo(queries *database.Queries)ChatRepo{
	return &chatRepo{
		quries: queries,
	}
}

//func (r *chatRepo)


