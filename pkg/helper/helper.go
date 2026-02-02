package helper

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

//tempory thing for mq fail
func SaveIntoLog(jobName string, job interface{}, logger *slog.Logger) {
	saveLog := []byte(fmt.Sprintf("jobName:%v;jobDescrioption:%v;\n", jobName, job))
	path := filepath.Join("../../", "consistency_log.txt")
	f, err := os.Create(path) //create the path
	defer f.Close()
	if err != nil {
		logger.Error("file create failed", err)
	}
	err = os.WriteFile(path, saveLog, 0644)
	if err != nil {
		logger.Error("file wirte process failed", err)
	}
}
