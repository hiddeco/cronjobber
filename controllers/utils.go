package controllers

import (
	"time"

	cronjobberv1alpha1 "github.com/hiddeco/cronjobber/api/v1alpha1"
)

func getCurrentTimeInZone(sj *cronjobberv1alpha1.TZCronJob) (time.Time, error) {
	if sj.Spec.TimeZone == "" {
		return time.Now(), nil
	}

	loc, err := time.LoadLocation(sj.Spec.TimeZone)
	if err != nil {
		return time.Now(), err
	}

	return time.Now().In(loc), nil
}
