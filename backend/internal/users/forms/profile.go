package forms

type UpdateUserInfoForm struct {
	Mobile string `form:"mobile" json:"mobile"`
	Email  string `form:"email" json:"email"`
}

type ChangeUserPasswordForm struct {
	OldPassword     string `form:"old_password" json:"old_password" binding:"required,min=3,max=32"`
	NewPassword     string `form:"new_password" json:"new_password" binding:"required,min=7,max=32"`
	ConfirmPassword string `form:"confirm_password" json:"confirm_password" binding:"required,min=7,max=32"`
}
